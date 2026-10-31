package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// homeScreen is the home page: one card per recorded server, no instances
// to pick here (docs/UI.md 9.2).
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
	return menu("enter", "进入服务器", "a", "添加服务器", "r", "刷新", "s", "设置")
}

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
		if c, ok := moveGrid(h.cursor, len(h.hosts), gridCols(m.width), k); ok {
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
		return box("", "还没有添加服务器。\n\n"+
			"Valise 通过你电脑上已经配置好的 SSH 连接服务器，不保存任何密码或私钥。\n"+
			"按 "+keyStyle.Render("a")+" 添加第一台服务器（可以从 ~/.ssh/config 里选）。", min(m.width-2, 80))
	}
	var cards []string
	for i, host := range h.hosts {
		cards = append(cards, serverCard(m, m.server(host), i == h.cursor))
	}
	return grid(cards, h.cursor, m.width, m.bodyHeight())
}

// serverCard summarizes one server: reachability, agents, the oldest
// "last escape pod", cloud backup and what needs attention.
func serverCard(m *Model, d *srvData, selected bool) string {
	var b strings.Builder
	state := dimStyle.Render("● 连接中…")
	switch {
	case d.err != nil:
		state = errStyle.Render("● 连不上")
	case d.loaded:
		state = okStyle.Render("● 在线")
	}
	name := headStyle.Render(d.host)
	pad := cardWidth - 4 - lipgloss.Width(name) - lipgloss.Width(state)
	b.WriteString(name + strings.Repeat(" ", max(1, pad)) + state + "\n")
	if !d.loaded || d.err != nil {
		if d.err != nil {
			b.WriteString(dimStyle.Render(firstLine(errText(d.err))))
		}
		return cardStyle(selected, false, cardWidth).Render(b.String())
	}
	now := m.now()
	var live []string
	oldest := time.Time{}
	never := 0
	attention := 0
	for _, in := range d.instances {
		if in.Superseded != nil {
			continue
		}
		color := cDim
		if dp := m.display(in.ProfileID); dp != nil && dp.Color != "" {
			color = lipgloss.Color(dp.Color)
		}
		live = append(live, lipgloss.NewStyle().Foreground(color).Render("■")+" "+in.InstanceID)
		t, ok := d.lastSeal(in.InstanceID)
		switch {
		case !ok:
			never++
			attention++
		case oldest.IsZero() || t.Before(oldest):
			oldest = t
		}
		if ok && now.Sub(t) > 7*24*time.Hour {
			attention++
		}
	}
	b.WriteString(dimStyle.Render(itoa(len(live))+" 个 agent") + "\n")
	b.WriteString(strings.Join(live, "  ") + "\n\n")
	last := "—"
	switch {
	case never > 0:
		last = warnStyle.Render(itoa(never) + " 个还没有逃生舱")
	case !oldest.IsZero():
		last = ago(oldest, now)
	}
	cloud := warnStyle.Render("未设置云备份")
	if d.status != nil && len(d.status.Targets) > 0 {
		cloud = ""
		for _, t := range d.status.Targets {
			s := t.Name + " " + dimStyle.Render("从未同步")
			if t.LastOK != nil {
				s = t.Name + " " + ago(*t.LastOK, now)
			}
			if t.LastError != "" {
				s = t.Name + " " + errStyle.Render("上次失败")
				attention++
			}
			cloud += s + "  "
		}
	}
	need := okStyle.Render("没有")
	if attention > 0 {
		need = warnStyle.Render(itoa(attention) + " 件事")
	}
	b.WriteString(kv("最久没建舱", last, "云备份", strings.TrimSpace(cloud), "需要注意", need))
	return cardStyle(selected, false, cardWidth).Render(b.String())
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
