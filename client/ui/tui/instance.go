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

// instancePage is one agent: a header and five tabs (docs/UI.md 9.5).
type instancePage struct {
	host   string
	inst   enroll.InstanceSummary
	active int
	tabs   []tab
	scroll int // offset of tabs that do not scroll themselves
	// focus..focusEnd: the lines of the selected item in the tab's
	// content, set by the tab's view (-1: none); scrolling follows it.
	focus, focusEnd int
	// Shared data, loaded once and refreshed by the tabs that change it.
	target *service.LoginTarget
	kit    *agentkit.KitStatusResult
	check  *agentkit.CheckupResult
	log    map[string]vaultrpc.SealLogEntry
	health *service.HealthReport
	// healthErr: the health report could not be read.
	healthErr error
}

func newInstancePage(host string, in enroll.InstanceSummary) *instancePage {
	p := &instancePage{host: host, inst: in, log: map[string]vaultrpc.SealLogEntry{}}
	p.tabs = []tab{&overviewTab{}, &manageTab{}, &healthTab{}, newPodsTab(), &isetTab{}}
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
func (p *instancePage) typing() bool {
	t, ok := p.tabs[p.active].(interface{ typing() bool })
	return ok && t.typing()
}
func (p *instancePage) hostName() string { return p.host }

// flourish follows the tab; the overview has none while something needs
// the user.
func (p *instancePage) flourish(m *Model) string {
	switch p.tabs[p.active].(type) {
	case *overviewTab:
		if len(attention(m, m.server(p.host), p.inst)) == 0 {
			return flourishOverview
		}
	case *manageTab:
		return flourishManage
	case *podsTab:
		return flourishPods
	}
	return ""
}
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
		p.loadHealth(m),
	}
	return tea.Batch(append(cmds, p.tabs[p.active].load(m, p))...)
}

func (p *instancePage) loadLog(m *Model) tea.Cmd {
	svc, ctx, host, inst := m.svc, m.ctx, p.host, p.inst.InstanceID
	return func() tea.Msg { l, _ := svc.SealLog(ctx, host, inst); return logLoaded{l} }
}

func (p *instancePage) loadHealth(m *Model) tea.Cmd {
	svc, ctx, host, inst := m.svc, m.ctx, p.host, p.inst.InstanceID
	return func() tea.Msg { r, err := svc.Health(ctx, host, inst); return healthLoaded{r, err} }
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
	case healthLoaded:
		p.health, p.healthErr = msg.r, msg.err
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
	cmd, handled := p.tabs[p.active].update(m, p, msg)
	if k, ok := msg.(tea.KeyPressMsg); ok && !handled {
		// A tab without a cursor here: the arrows scroll its content.
		switch k.String() {
		case "down", "j":
			p.scroll++
			return cmd, true
		case "up", "k":
			p.scroll = max(0, p.scroll-1)
			return cmd, true
		}
	}
	return cmd, handled
}

// setFocus records the selected item's lines (tabs call it from view).
func (p *instancePage) setFocus(from, to int) { p.focus, p.focusEnd = from, max(from, to) }

func (p *instancePage) switchTo(m *Model, i int) tea.Cmd {
	p.active, p.scroll = i, 0
	return p.tabs[i].load(m, p)
}

func (p *instancePage) view(m *Model) string {
	icon := compactIcon(m.display(p.inst.ProfileID))
	name := displayName(m, p.inst.ProfileID)
	if v := p.version(); v != "" {
		name += " " + v
	}
	info := []string{headStyle.Render(p.inst.InstanceID) + dimStyle.Render(" · "+name+" · "+p.host), runState(p.inst)}
	if len(p.inst.Entries) > 0 {
		e := p.inst.Entries[0]
		info = append(info, "管理页：http://127.0.0.1:"+itoa(e.Port)+" "+mark(e.Listening, true))
	}
	if p.inst.ProfileStatus != "verified" {
		info = append(info, faintStyle.Render("profile 未经审核或尚未完整核实（见概览里的支持程度）"))
	}
	head := lipgloss.JoinHorizontal(lipgloss.Top, strings.Join(icon, "\n"), "  ", strings.Join(info, "\n"))
	names := make([]string, len(p.tabs))
	for i, t := range p.tabs {
		names[i] = t.name()
	}
	top := head + "\n" + tabs(names, p.active) + "\n" + dimStyle.Render(strings.Repeat("─", min(m.width, 100))) + "\n"
	height := m.bodyHeight() - lipgloss.Height(top)
	p.focus, p.focusEnd = -1, -1
	content := p.tabs[p.active].view(m, p, height)
	lines := strings.Split(content, "\n")
	if len(lines) > height {
		room := height - 1
		if p.focus >= 0 {
			// Keep the selected item on screen, its top first.
			if p.focusEnd >= p.scroll+room {
				p.scroll = p.focusEnd - room + 1
			}
			if p.focus < p.scroll {
				p.scroll = p.focus
			}
		}
		p.scroll = max(0, min(p.scroll, len(lines)-room))
		end := min(len(lines), p.scroll+room)
		content = strings.Join(lines[p.scroll:end], "\n") + "\n" + faintStyle.Render("  ↑↓ 移动 · pgdn/pgup 翻页（"+itoa(end)+" / "+itoa(len(lines))+" 行）")
	} else {
		p.scroll = 0
	}
	return top + content
}

// version is the harness version from the recorded install source.
func (p *instancePage) version() string {
	if p.target == nil || p.target.Record == nil {
		return ""
	}
	pv := p.target.Record.Provenance
	switch {
	case pv.NPM != nil:
		return pv.NPM.Version
	case pv.Tool != nil:
		return pv.Tool.Version
	}
	return ""
}

// overviewTab: is it running, how to open it, does anything need the
// user (docs/UI.md 9.5 "概览").
type overviewTab struct{ cursor int }

// quick is one quick action of the overview.
type quick struct {
	label string
	do    func(m *Model, p *instancePage) tea.Cmd
}

func (o *overviewTab) quicks(p *instancePage) []quick {
	var qs []quick
	host, inst := p.host, p.inst.InstanceID
	if len(p.inst.Entries) > 0 {
		qs = append(qs, quick{"打开管理页（浏览器，需要令牌时自动带上）", func(m *Model, p *instancePage) tea.Cmd { return m.runCLI("open", host, inst) }})
	}
	if p.target != nil && len(p.target.Profile.NativeCLI) > 0 {
		pid := p.target.Profile.ID
		qs = append(qs, quick{"原生界面（" + strings.Join(p.target.Profile.NativeCLI, " ") + "，高级）", func(m *Model, p *instancePage) tea.Cmd {
			return gatedCLI(m, host, "native", host, "--profile", pid)
		}})
	}
	return append(qs, quick{"建立逃生舱", func(m *Model, p *instancePage) tea.Cmd { return startSeal(m, p) }})
}

func (o *overviewTab) name() string { return "概览" }
func (o *overviewTab) help(p *instancePage) string {
	return menu("↑↓", "选择", "enter", "执行", "b", "建立逃生舱")
}
func (o *overviewTab) load(m *Model, p *instancePage) tea.Cmd { return nil }
func (o *overviewTab) update(m *Model, p *instancePage, msg tea.Msg) (tea.Cmd, bool) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil, false
	}
	qs := o.quicks(p)
	if c, moved := moveList(o.cursor, len(qs), k.String()); moved {
		o.cursor = c
		return nil, true
	}
	switch k.String() {
	case "enter":
		return qs[min(o.cursor, len(qs)-1)].do(m, p), true
	case "b":
		return startSeal(m, p), true
	}
	return nil, false
}

// capLabels are the plain names of profile capabilities (PROFILES.md 1.2).
var capLabels = []struct{ name, label string }{
	{"install", "安装"}, {"manage", "管理"}, {"backup", "备份"}, {"restore", "着陆"}, {"login", "登录"},
}

func capMark(status string) string {
	switch status {
	case "tested":
		return okStyle.Render("✓已测")
	case "partial":
		return warnStyle.Render("◐部分")
	case "failed":
		return errStyle.Render("✗失败")
	}
	return dimStyle.Render("·未测")
}

func (o *overviewTab) view(m *Model, p *instancePage, height int) string {
	d := m.server(p.host)
	var b strings.Builder
	// The health page's one or two most important items (docs/UI.md 9.5).
	if top := topHealth(m, p, 2); len(top) > 0 {
		for _, it := range top {
			st := warnStyle
			if it.Level == service.HealthBad {
				st = errStyle
			}
			b.WriteString(st.Render(levelMarkPlain(it.Level)+" "+it.Title+"："+it.Text) + dimStyle.Render("  · 详见 3 健康") + "\n")
		}
		b.WriteString("\n")
	}
	attn := attention(m, d, p.inst)
	if len(attn) == 0 {
		b.WriteString(dimStyle.Render("需要处理：无") + "\n\n")
	} else {
		b.WriteString(warnStyle.Render("需要处理") + "\n")
		for _, a := range attn {
			b.WriteString(warnStyle.Render("  ! "+a) + "\n")
		}
		b.WriteString("\n")
	}
	b.WriteString(headStyle.Render("快捷操作") + "\n")
	qs := o.quicks(p)
	o.cursor = min(o.cursor, len(qs)-1)
	for i, q := range qs {
		if i == o.cursor {
			n := strings.Count(b.String(), "\n")
			p.setFocus(n, n)
		}
		b.WriteString("  " + cursorLine(i == o.cursor, q.label) + "\n")
	}
	b.WriteString("\n")
	if p.target != nil {
		var caps []string
		for _, c := range capLabels {
			caps = append(caps, c.label+" "+capMark(p.target.Profile.CapabilityStatus(c.name)))
		}
		rows := []string{"支持程度", strings.Join(caps, "  ")}
		if p.target.Local {
			rows = append(rows, "", dimStyle.Render("本地 profile，未经审核"))
		}
		if p.target.Record != nil {
			rows = append(rows, "安装来源", provenanceText(p.target.Record.Provenance))
		}
		backup := backupLine(m, d, p.inst, true)
		if p.check != nil && len(p.check.Findings) > 0 {
			backup += dimStyle.Render("  ·  ") + warnStyle.Render("体检有 "+itoa(len(p.check.Findings))+" 个提醒（见体检）")
		}
		rows = append(rows, "备份", backup)
		if p.kit != nil && p.kit.LastMCP != nil {
			rows = append(rows, "agent 上次联系", ago(*p.kit.LastMCP, m.now())+dimStyle.Render("，通过逃生舱技能"))
		}
		b.WriteString(kv(rows...))
	}
	return b.String()
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
