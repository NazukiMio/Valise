package tui

import (
	tea "charm.land/bubbletea/v2"

	"valise/core/enroll"
)

// serverPage shows one server's instances as paged cards (docs/UI.md 8.1,
// 9.4).
type serverPage struct {
	host   string
	cursor int
}

func newServerPage(host string) *serverPage { return &serverPage{host: host} }

func (s *serverPage) title() string    { return s.host }
func (s *serverPage) hostName() string { return s.host }
func (s *serverPage) help() string {
	return menu("←→", "选择", "enter", "进入", "i", "安装 harness", "n", "接入 Valise", "c", "空间站", "k", "空间清理", "t", "服务器设置", "r", "刷新")
}

func (s *serverPage) flourish(m *Model) string {
	return flourishServer(len(live(m.server(s.host).instances)))
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
	if c, moved := movePager(s.cursor, len(d.instances), m.width, s.cardWidth(m), k.String()); moved {
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
	case "i":
		return s.install(m), true
	case "n":
		return s.connect(m), true
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

func (s *serverPage) cardWidth(m *Model) int {
	if compactCards(m.width, m.height) {
		return compactCardW
	}
	return cardW
}

// connect shows the report of connecting an agent to Valise in the
// terminal, then asks (PRODUCT.md 8.1).
func (s *serverPage) connect(m *Model) tea.Cmd {
	host := s.host
	return tea.Sequence(m.runCLI("enroll", host), func() tea.Msg {
		return pushMsg{confirm("接受上面的接入报告，把这个 agent 接入 Valise？", []string{
			"接入只读取和记录：在服务器上写一份实例记录，不改动 agent 的任何文件，也不往 harness 里安装任何东西。",
			"如果报告里有多个候选或需要补充归类，请在终端用 valise enroll 的参数处理。",
		}, func(m *Model) tea.Cmd {
			return tea.Sequence(m.runCLI("enroll", host, "--accept"), m.loadServer(host))
		})}
	})
}

// install picks a harness with an official plan and runs the install in
// the terminal (docs/UI.md 9.6, docs/INSTALL.md section 3).
func (s *serverPage) install(m *Model) tea.Cmd {
	var cs []choice
	if m.prof != nil {
		for id, lp := range m.prof.ByID {
			if lp.Effective.Install != nil {
				cs = append(cs, choice{label: lp.Effective.DisplayName, value: id, desc: lp.Effective.Install.Summary})
			}
		}
	}
	if len(cs) == 0 {
		return func() tea.Msg {
			return flashMsg("还没有可以安装的 harness（资料包里没有官方方案）")
		}
	}
	host := s.host
	return m.push(pick("选择要安装的 harness", cs, func(m *Model, id string) tea.Cmd {
		return tea.Sequence(m.runCLI("install", host, "--profile", id), m.loadServer(host))
	}))
}

// pushMsg asks the root to push a screen (from a command chain).
type pushMsg struct{ s screen }

// live drops superseded instances.
func live(ins []enroll.InstanceSummary) []enroll.InstanceSummary {
	var out []enroll.InstanceSummary
	for _, in := range ins {
		if in.Superseded == nil {
			out = append(out, in)
		}
	}
	return out
}

func (s *serverPage) view(m *Model) string {
	d := m.server(s.host)
	switch {
	case d.err != nil:
		return errText(d.err)
	case !d.loaded:
		return dimStyle.Render("正在连接 " + s.host + " …")
	case len(d.instances) == 0:
		return box("", "这台服务器上还没有接入 Valise 的 agent。\n\n按 "+keyStyle.Render("n")+" 把已经装好的 agent 接入 Valise，或按 "+
			keyStyle.Render("i")+" 安装一个新的 harness。", min(m.width-2, 70)) + "\n\n" + subtitle("这台服务器上的 agent 还没塞进 Valise。")
	}
	compact := compactCards(m.width, m.height)
	var cards []string
	for i, in := range d.instances {
		cards = append(cards, instanceCard(m, d, in, i == s.cursor, compact))
	}
	head := dimStyle.Render(s.host + " · 在线 · " + itoa(len(live(d.instances))) + " 个 agent")
	return head + "\n\n" + pager(cards, s.cursor, min(m.width, 160), s.cardWidth(m))
}
