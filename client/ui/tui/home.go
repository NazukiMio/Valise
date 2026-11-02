package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// homeScreen is the home page: one full-width card per recorded server,
// stacked; no instances to pick here (docs/UI.md 9.2).
type homeScreen struct {
	hosts  []string
	cursor int
	err    error
}

type hostsLoaded struct {
	hosts []string
	err   error
}

func newHome() *homeScreen { return &homeScreen{} }

func (h *homeScreen) title() string { return "首页" }
func (h *homeScreen) help() string {
	if len(h.hosts) == 0 {
		return menu("a", "添加服务器", "s", "设置")
	}
	return menu("↑↓", "选择", "enter", "进入服务器", "a", "添加服务器", "g", "第一次使用", "r", "刷新", "s", "设置")
}

func (h *homeScreen) flourish(m *Model) string { return flourishHome(len(h.hosts)) }

func (h *homeScreen) init(m *Model) tea.Cmd {
	svc := m.svc
	return func() tea.Msg {
		hosts, err := svc.Servers()
		return hostsLoaded{hosts, err}
	}
}

func (h *homeScreen) update(m *Model, msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case hostsLoaded:
		h.hosts, h.err = msg.hosts, msg.err
		var cmds []tea.Cmd
		for _, host := range h.hosts {
			if d := m.server(host); !d.loaded && !d.loading {
				cmds = append(cmds, m.loadServer(host))
			}
		}
		return tea.Batch(cmds...), true
	case tea.KeyPressMsg:
		k := msg.String()
		if k == "g" { // before moveList, where g jumps to the top
			return m.push(newChecklist(h.hosts)), true
		}
		if c, ok := moveList(h.cursor, len(h.hosts), k); ok {
			h.cursor = c
			return nil, true
		}
		switch k {
		case "enter":
			if len(h.hosts) > 0 {
				return m.push(newServerPage(h.hosts[h.cursor])), true
			}
		case "a":
			return m.push(newAddServer(func(m *Model) tea.Cmd { return h.init(m) })), true
		case "s":
			return m.push(newSettings()), true
		case "r":
			var cmds []tea.Cmd
			for _, host := range h.hosts {
				cmds = append(cmds, m.loadServer(host))
			}
			return tea.Batch(append(cmds, h.init(m))...), true
		}
	}
	return nil, false
}

func (h *homeScreen) view(m *Model) string {
	if h.err != nil {
		return errText(h.err)
	}
	if len(h.hosts) == 0 {
		return subtitle(flourishHome(0)) + "\n\n" + box("", "还没有添加服务器。\n\n"+
			"Valise 在你的服务器上安装和管理 agent，并替它做好备份。\n"+
			"按 "+keyStyle.Render("a")+" 添加你的第一台服务器（手动填写，或从 ~/.ssh/config 导入）。", min(m.width-2, 80))
	}
	w := min(m.width-2, 100)
	var cards []string
	for i, host := range h.hosts {
		cards = append(cards, serverCard(m, m.server(host), i == h.cursor, w))
	}
	head := dimStyle.Render("你的服务器（"+itoa(len(h.hosts))+" 台）") + "\n\n"
	gc := guideCard(m, h.hosts, w)
	if gc == "" {
		return head + vstack(cards, h.cursor, m.bodyHeight()-2)
	}
	return head + vstack(cards, h.cursor, m.bodyHeight()-2-lipgloss.Height(gc)) + "\n" + gc
}

// vstack stacks cards, dropping whole cards above the selection until it
// fits in height lines.
func vstack(cards []string, cursor, height int) string {
	from := 0
	for from < cursor && sumHeight(cards[from:cursor+1]) > height {
		from++
	}
	out := strings.Join(cards[from:], "\n")
	if from > 0 {
		out = faintStyle.Render("  ↑ 上面还有 "+itoa(from)+" 台") + "\n" + out
	}
	return out
}

// serverCard: name and reachability, one line per agent, what needs the
// user, and one quiet backup line (docs/UI.md 9.2).
func serverCard(m *Model, d *srvData, selected bool, w int) string {
	state := dimStyle.Render("连接中…")
	switch {
	case d.err != nil:
		state = errStyle.Render("连不上")
	case d.loaded:
		state = okStyle.Render("在线")
	}
	agents := live(d.instances)
	head := headStyle.Render(d.host) + dimStyle.Render(" · ") + state
	if d.loaded && d.err == nil {
		head += dimStyle.Render(" · " + itoa(len(agents)) + " 个 agent")
	}
	var lines []string
	if d.err != nil {
		lines = append(lines, dimStyle.Render(firstLine(errText(d.err))))
	}
	var issues []string
	oldest, never, stationBad := time.Time{}, false, false
	for _, in := range agents {
		row := "  " + instName(in) + "  " + runState(in)
		if len(in.Entries) > 0 {
			row += dimStyle.Render(" · ") + entryState(in)
		}
		lines = append(lines, row)
		for _, a := range attention(m, d, in) {
			issues = append(issues, in.InstanceID+"："+a)
		}
		t, ok := d.lastSeal(in.InstanceID)
		switch {
		case !ok:
			never = true
		case oldest.IsZero() || t.Before(oldest):
			oldest = t
		}
		if d.status != nil && len(d.status.Targets) > 0 && !d.synced(lastSnapshotID(d, in.InstanceID)) {
			stationBad = true
		}
	}
	if d.status != nil {
		for _, t := range d.status.Targets {
			if t.LastError != "" {
				issues = append(issues, "空间站 "+t.Name+" 上次同步失败")
			}
		}
	}
	if len(issues) > 0 {
		need := warnStyle.Render("需要处理 " + itoa(len(issues)) + " 件事")
		head += strings.Repeat(" ", max(2, w-4-lipgloss.Width(head)-lipgloss.Width(need))) + need
		lines = append(lines, warnStyle.Render("  ! "+strings.Join(issues[:min(2, len(issues))], "  ! ")))
	}
	if len(agents) > 0 && !never && !oldest.IsZero() {
		backup := "  备份正常 · " + ago(oldest, m.now())
		if d.status != nil && len(d.status.Targets) > 0 {
			backup += " · 空间站 " + mark(!stationBad, true)
		}
		lines = append(lines, dimStyle.Render(backup))
	}
	body := head + "\n" + fitLines(lines, w-4, 6)
	b, c := lipgloss.RoundedBorder(), cLine
	if selected {
		b, c = lipgloss.ThickBorder(), cSelLine
	}
	return lipgloss.NewStyle().Border(b).BorderForeground(c).Padding(0, 1).Width(w).Render(body)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
