package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"valise/core/enroll"
)

// serverPage shows one server's instances as cards (docs/UI.md 9.3).
type serverPage struct {
	host   string
	cursor int
}

func newServerPage(host string) *serverPage { return &serverPage{host: host} }

func (s *serverPage) title() string { return s.host }
func (s *serverPage) help() string {
	return menu("enter", "进入", "n", "登记新 agent", "c", "云备份", "k", "空间清理", "t", "服务器设置", "r", "刷新")
}

func (s *serverPage) init(m *Model) tea.Cmd {
	if d := m.server(s.host); !d.loaded && !d.loading {
		return m.loadServer(s.host)
	}
	return nil
}

func (s *serverPage) update(m *Model, msg tea.Msg) (tea.Cmd, bool) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil, false
	}
	d := m.server(s.host)
	if c, moved := moveGrid(s.cursor, len(d.instances), gridCols(m.width), k.String()); moved {
		s.cursor = c
		return nil, true
	}
	switch k.String() {
	case "enter":
		if s.cursor < len(d.instances) {
			in := d.instances[s.cursor]
			if in.Superseded != nil {
				return func() tea.Msg {
					return flashMsg("这个实例已被 " + in.Superseded.By + " 取代；可以在空间清理（k）里删除它")
				}, true
			}
			return m.push(newInstancePage(s.host, in)), true
		}
	case "n":
		return s.enroll(m), true
	case "c":
		return m.push(newCloud(s.host)), true
	case "k":
		return m.push(newCleanup(s.host)), true
	case "t":
		return m.push(newServerSettings(s.host)), true
	case "r":
		return m.loadServer(s.host), true
	default:
		return nil, false
	}
	return nil, true
}

// enroll shows the enrollment report in the terminal, then asks.
func (s *serverPage) enroll(m *Model) tea.Cmd {
	host := s.host
	return tea.Sequence(m.runCLI("enroll", host), func() tea.Msg {
		return pushMsg{confirm("接受上面的登记报告，把这个 agent 登记到 "+host+"？", []string{
			"登记只在服务器上写一份实例记录，不改动 agent 的任何文件。",
			"如果报告里有多个候选或需要补充归类，请在终端用 valise enroll 的参数处理。",
		}, func(m *Model) tea.Cmd {
			return tea.Sequence(m.runCLI("enroll", host, "--accept"), m.loadServer(host))
		})}
	})
}

// pushMsg asks the root to push a screen (from a command chain).
type pushMsg struct{ s screen }

func (s *serverPage) view(m *Model) string {
	d := m.server(s.host)
	switch {
	case d.err != nil:
		return errText(d.err)
	case !d.loaded:
		return dimStyle.Render("正在连接 " + s.host + " …")
	case len(d.instances) == 0:
		return box("", "这台服务器上还没有登记的 agent。按 "+keyStyle.Render("n")+" 登记。", min(m.width-2, 70))
	}
	var cards []string
	for i, in := range d.instances {
		cards = append(cards, instanceCard(m, d, in, i == s.cursor))
	}
	return grid(cards, s.cursor, m.width, m.bodyHeight())
}

// instanceCard: icon on the left, the essentials on the right.
func instanceCard(m *Model, d *srvData, in enroll.InstanceSummary, selected bool) string {
	icon := renderIcon(m.display(in.ProfileID))
	name := in.ProfileID
	if m.prof != nil && m.prof.ByID[in.ProfileID] != nil {
		name = m.prof.ByID[in.ProfileID].Effective.DisplayName
	}
	var info []string
	info = append(info, headStyle.Render(in.InstanceID), dimStyle.Render(name))
	if in.Superseded != nil {
		info = append(info, "", warnStyle.Render("已被取代"), dimStyle.Render("被 "+in.Superseded.By+" 取代"))
	} else {
		info = append(info, "", runState(in))
		now := m.now()
		if t, ok := d.lastSeal(in.InstanceID); ok {
			cloud := warnStyle.Render("☁ 未上云")
			if sid := lastSnapshotID(d, in.InstanceID); d.synced(sid) {
				cloud = okStyle.Render("☁")
			}
			info = append(info, "逃生舱 "+ago(t, now)+" "+cloud)
		} else {
			info = append(info, warnStyle.Render("还没有逃生舱"))
		}
		if in.ProfileStatus != "verified" {
			info = append(info, faintStyle.Render("profile 未核实"))
		}
	}
	iconW := 0
	for _, l := range icon {
		iconW = max(iconW, lipgloss.Width(l))
	}
	left := lipgloss.NewStyle().Width(iconW + 2).Render(strings.Join(icon, "\n"))
	right := lipgloss.NewStyle().Width(max(10, cardWidth-4-iconW-2)).Render(strings.Join(info, "\n"))
	return cardStyle(selected, in.Superseded != nil, cardWidth).Render(lipgloss.JoinHorizontal(lipgloss.Top, left, right))
}

func runState(in enroll.InstanceSummary) string {
	if len(in.Service) == 0 {
		return dimStyle.Render("● 没有服务（前台运行）")
	}
	for _, s := range in.Service {
		if s.Active == "active" {
			return okStyle.Render("● 运行中")
		}
	}
	return warnStyle.Render("● 已停止")
}

func lastSnapshotID(d *srvData, inst string) string {
	best := ""
	if d.status == nil {
		return ""
	}
	for _, s := range d.status.Snapshots {
		if s.InstanceID == inst && s.SnapshotID > best {
			best = s.SnapshotID
		}
	}
	return best
}
