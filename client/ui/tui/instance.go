package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"valise/core/agentkit"
	"valise/core/enroll"
	"valise/core/vaultrpc"
	"valise/ui/service"
)

// tab is one section of the instance page.
type tab interface {
	name() string
	help(p *instancePage) string
	load(m *Model, p *instancePage) tea.Cmd
	update(m *Model, p *instancePage, msg tea.Msg) (tea.Cmd, bool)
	view(m *Model, p *instancePage, height int) string
}

// instancePage is one agent: a header card and five tabs (docs/UI.md 9.4).
type instancePage struct {
	host   string
	inst   enroll.InstanceSummary
	active int
	tabs   []tab
	scroll int // offset of tabs that do not scroll themselves
	// Shared data, loaded once and refreshed by the tabs that change it.
	target *service.LoginTarget
	kit    *agentkit.KitStatusResult
	check  *agentkit.CheckupResult
	log    map[string]vaultrpc.SealLogEntry
}

func newInstancePage(host string, in enroll.InstanceSummary) *instancePage {
	p := &instancePage{host: host, inst: in, log: map[string]vaultrpc.SealLogEntry{}}
	p.tabs = []tab{&overviewTab{}, newPodsTab(), &checkupTab{}, &webTab{}, &isetTab{}}
	return p
}

// Messages carrying the shared data.
type (
	targetLoaded struct{ t *service.LoginTarget }
	kitLoaded    struct{ k *agentkit.KitStatusResult }
	checkLoaded  struct {
		c   *agentkit.CheckupResult
		err error
	}
	logLoaded struct{ l *vaultrpc.SealLogResult }
)

func (p *instancePage) title() string { return p.inst.InstanceID }
func (p *instancePage) help() string {
	return p.tabs[p.active].help(p) + "   " + menu("←→", "切换")
}

func (p *instancePage) init(m *Model) tea.Cmd {
	svc, ctx, host, inst := m.svc, m.ctx, p.host, p.inst.InstanceID
	cmds := []tea.Cmd{
		func() tea.Msg { t, _ := svc.LoginMethods(ctx, host, inst); return targetLoaded{t} },
		func() tea.Msg { k, _ := svc.KitStatus(ctx, host, inst); return kitLoaded{k} },
		p.loadLog(m),
		p.loadCheckup(m),
	}
	return tea.Batch(append(cmds, p.tabs[p.active].load(m, p))...)
}

func (p *instancePage) loadLog(m *Model) tea.Cmd {
	svc, ctx, host, inst := m.svc, m.ctx, p.host, p.inst.InstanceID
	return func() tea.Msg { l, _ := svc.SealLog(ctx, host, inst); return logLoaded{l} }
}

func (p *instancePage) loadCheckup(m *Model) tea.Cmd {
	svc, ctx, host, inst := m.svc, m.ctx, p.host, p.inst.InstanceID
	return func() tea.Msg { c, err := svc.Checkup(ctx, host, inst); return checkLoaded{c, err} }
}

func (p *instancePage) update(m *Model, msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case targetLoaded:
		p.target = msg.t
	case kitLoaded:
		p.kit = msg.k
	case checkLoaded:
		if msg.err == nil {
			p.check = msg.c
		}
	case sealDone:
		return sealResult(m, p, msg), true
	case logLoaded:
		if msg.l != nil {
			for _, e := range msg.l.Entries {
				p.log[e.SnapshotID] = e
			}
		}
	case tea.KeyPressMsg:
		if b, ok := p.tabs[p.active].(interface{ typing() bool }); !ok || !b.typing() {
			k := msg.String()
			switch {
			case k == "right" || k == "tab":
				return p.switchTo(m, (p.active+1)%len(p.tabs)), true
			case k == "left" || k == "shift+tab":
				return p.switchTo(m, (p.active+len(p.tabs)-1)%len(p.tabs)), true
			case len(k) == 1 && k[0] >= '1' && int(k[0]-'1') < len(p.tabs):
				return p.switchTo(m, int(k[0]-'1')), true
			case k == "pgdown" || k == "space":
				p.scroll += max(1, m.bodyHeight()/2)
				return nil, true
			case k == "pgup":
				p.scroll = max(0, p.scroll-max(1, m.bodyHeight()/2))
				return nil, true
			}
		}
	}
	return p.tabs[p.active].update(m, p, msg)
}

func (p *instancePage) switchTo(m *Model, i int) tea.Cmd {
	p.active, p.scroll = i, 0
	return p.tabs[i].load(m, p)
}

func (p *instancePage) view(m *Model) string {
	icon := renderSmall(m, p)
	name := p.inst.ProfileID
	if m.prof != nil && m.prof.ByID[p.inst.ProfileID] != nil {
		name = m.prof.ByID[p.inst.ProfileID].Effective.DisplayName
	}
	verified := warnStyle.Render("profile 未核实")
	if p.inst.ProfileStatus == "verified" {
		verified = okStyle.Render("profile 已核实")
	}
	info := headStyle.Render(p.inst.InstanceID) + "  " + dimStyle.Render(name+" · "+p.host) + "\n" + runState(p.inst) + "   " + verified
	head := lipgloss.JoinHorizontal(lipgloss.Top, strings.Join(icon, "\n"), "  ", info)
	names := make([]string, len(p.tabs))
	for i, t := range p.tabs {
		names[i] = t.name()
	}
	top := head + "\n\n" + tabs(names, p.active) + "\n" + dimStyle.Render(strings.Repeat("─", min(m.width, 100))) + "\n"
	height := m.bodyHeight() - lipgloss.Height(top)
	content := p.tabs[p.active].view(m, p, height)
	lines := strings.Split(content, "\n")
	if len(lines) > height {
		p.scroll = min(p.scroll, len(lines)-height+1)
		end := min(len(lines), p.scroll+height-1)
		content = strings.Join(lines[p.scroll:end], "\n") + "\n" + faintStyle.Render("  pgdn/空格 下翻 · pgup 上翻（"+itoa(end)+" / "+itoa(len(lines))+" 行）")
	} else {
		p.scroll = 0
	}
	return top + content
}

// renderSmall is the header icon, at most 10×4: braille art is scaled by
// dots; other art is shown only when it already fits (sampling it down
// garbles it), otherwise a colored marker stands in.
func renderSmall(m *Model, p *instancePage) []string {
	d := m.display(p.inst.ProfileID)
	if d == nil || strings.TrimSpace(d.Icon) == "" {
		return renderIcon(nil)[:4]
	}
	st := lipgloss.NewStyle().Foreground(cDim)
	if d.Color != "" {
		st = st.Foreground(lipgloss.Color(d.Color))
	}
	art := trimArt(strings.Split(strings.TrimRight(d.Icon, "\n"), "\n"))
	w := 0
	for _, r := range art {
		w = max(w, len(r))
	}
	var small []string
	switch {
	case w <= 10 && len(art) <= 4:
		small = toStrings(art)
	case isBraille(art):
		small = scaleBraille(art, w, 10, 4)
	default:
		small = []string{"██", "██"}
	}
	for i := range small {
		small[i] = st.Render(small[i])
	}
	return small
}

// overviewTab: the state at a glance (docs/UI.md 9.4 "概览").
type overviewTab struct{}

func (o *overviewTab) name() string { return "概览" }
func (o *overviewTab) help(p *instancePage) string {
	return menu("b", "建立逃生舱", "o", "打开管理页")
}
func (o *overviewTab) load(m *Model, p *instancePage) tea.Cmd { return nil }
func (o *overviewTab) update(m *Model, p *instancePage, msg tea.Msg) (tea.Cmd, bool) {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		switch k.String() {
		case "b":
			return startSeal(m, p), true
		case "o":
			return p.switchTo(m, 3), true
		}
	}
	return nil, false
}

func (o *overviewTab) view(m *Model, p *instancePage, height int) string {
	w := min(m.width-2, 100)
	now := m.now()
	d := m.server(p.host)
	source := dimStyle.Render("读取中…")
	if p.target != nil && p.target.Record != nil {
		source = provenanceText(p.target.Record.Provenance)
	}
	contact := dimStyle.Render("还没有（逃生舱技能未接入或 agent 还没用过）")
	if p.kit != nil && p.kit.LastMCP != nil {
		contact = ago(*p.kit.LastMCP, now) + dimStyle.Render("，通过逃生舱技能")
	}
	now1 := box("现在", kv("运行状态", runState(p.inst), "安装来源", source, "agent 上次联系", contact), w)
	pods := "还没有逃生舱。按 " + keyStyle.Render("b") + " 建立第一个。"
	if t, ok := d.lastSeal(p.inst.InstanceID); ok {
		n, unsynced := 0, 0
		for _, s := range d.status.Snapshots {
			if s.InstanceID == p.inst.InstanceID {
				n++
				if !d.synced(s.SnapshotID) {
					unsynced++
				}
			}
		}
		cloud := okStyle.Render("全部已上云")
		switch {
		case len(d.status.Targets) == 0:
			cloud = warnStyle.Render("未设置云备份")
		case unsynced > 0:
			cloud = warnStyle.Render(itoa(unsynced) + " 个还没上云")
		}
		pods = kv("最近一个", ago(t, now)+" · "+whoText(p.log[lastSnapshotID(d, p.inst.InstanceID)]), "一共", itoa(n)+" 个", "云备份", cloud)
	}
	check := dimStyle.Render("体检中…")
	if c := p.check; c != nil {
		if len(c.Findings) == 0 {
			check = okStyle.Render("✓ 健康")
		} else {
			check = warnStyle.Render("▲ "+itoa(len(c.Findings))+" 个提醒") + "：" + findingTitle(c.Findings[0].ID) + dimStyle.Render("（按 3 查看处理建议）")
		}
	}
	return now1 + "\n" + box("逃生舱", pods, w) + "\n" + box("体检", check, w)
}

func provenanceText(pv enroll.Provenance) string {
	switch {
	case pv.NPM != nil:
		return "npm 全局包 " + pv.NPM.Package + "@" + pv.NPM.Version
	case pv.Git != nil:
		s := "git " + pv.Git.Remote + " @ " + short(pv.Git.Commit)
		if pv.Git.Dirty {
			s += warnStyle.Render("（有本地修改）")
		}
		return s
	case pv.Tool != nil:
		return pv.Kind + " " + pv.Tool.Package + " " + pv.Tool.Version
	case pv.Binary != nil:
		return "程序 " + shortPath(pv.Binary.Path, 48)
	}
	return dimStyle.Render("未知")
}

// shortPath keeps the end of a long path.
func shortPath(p string, n int) string {
	r := []rune(p)
	if len(r) <= n {
		return p
	}
	return "…" + string(r[len(r)-n+1:])
}

func short(s string) string {
	if len(s) > 10 {
		return s[:10]
	}
	return s
}
