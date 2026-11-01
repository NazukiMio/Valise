package tui

import (
	tea "charm.land/bubbletea/v2"

	"valise/core/vaultrpc"
)

// syncScreen sends a server's escape pods to a cloud backup location (the
// vault is append-only: only what the location lacks is sent). The launch
// scene waits on the pad while it runs and lifts off when it succeeds.
type syncScreen struct {
	host, target string
	running      bool
	res          *vaultrpc.SyncResult
	err          error
	rocket       *rocketPlayer
	prog         progressBox
}

type syncDone struct {
	res *vaultrpc.SyncResult
	err error
}

func newSync(host, target string) *syncScreen {
	return &syncScreen{host: host, target: target, running: true}
}

// startSync picks the location: none yet opens the cloud backup page, one
// starts at once, several are chosen from.
func startSync(m *Model, host string) tea.Cmd {
	var names []string
	if st := m.server(host).status; st != nil {
		for _, t := range st.Targets {
			names = append(names, t.Name)
		}
	}
	switch len(names) {
	case 0:
		m.flash, m.flashErr = "这台服务器还没有云备份位置；先在这里添加一个（按 +）。", false
		return m.push(newCloud(host))
	case 1:
		return m.push(newSync(host, names[0]))
	}
	var cs []choice
	for _, n := range names {
		cs = append(cs, choice{label: n, value: n})
	}
	return m.push(pick("上云到哪个位置？", cs, func(m *Model, n string) tea.Cmd { return m.push(newSync(host, n)) }))
}

func (s *syncScreen) title() string    { return "上云" }
func (s *syncScreen) hostName() string { return s.host }
func (s *syncScreen) help() string {
	if s.running {
		return ""
	}
	return menu("esc", "完成")
}
func (s *syncScreen) busy() string {
	if s.running {
		return "正在上云，等它结束；中途断开不会损坏任何东西，但这次要重新开始"
	}
	return ""
}

func (s *syncScreen) init(m *Model) tea.Cmd {
	s.rocket = newRocket(sceneLaunch, m.now())
	svc, ctx, host, target := m.svc, m.ctx, s.host, s.target
	return func() tea.Msg { r, err := svc.Sync(ctx, host, target); return syncDone{r, err} }
}

func (s *syncScreen) animating(m *Model) bool {
	return s.rocket != nil && (s.running || !s.rocket.done(m.now()))
}

func (s *syncScreen) update(m *Model, msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case syncDone:
		s.running, s.res, s.err = false, msg.res, msg.err
		if s.err != nil {
			s.rocket = nil
			return nil, true
		}
		s.rocket.go_(m.now())
		return m.loadServer(s.host), true
	case tea.KeyPressMsg:
		if s.running {
			return nil, msg.String() == "esc" || msg.String() == "q"
		}
	}
	return nil, false
}

func (s *syncScreen) view(m *Model) string {
	head := headStyle.Render("上云：" + s.host + " → " + s.target)
	var text string
	switch {
	case s.running:
		text = head + "\n\n" + stepsView([]step{{"", "把还没上云的逃生舱传到 " + s.target}}, s.prog.get(), m.now(), s.rocket.start) +
			"\n" + dimStyle.Render("  传输由服务器直接发往云端，逃生舱一直是加密的；云端只追加，不改动已有的文件。")
	case s.err != nil:
		text = head + "\n\n" + errStyle.Render(errText(s.err))
	default:
		text = head + "\n\n" + okStyle.Render("✓ 已上云") + "：" + itoa(len(s.res.Leaves)) + " 个周期、" + itoa(len(s.res.Snapshots)) + " 个逃生舱" +
			"\n" + dimStyle.Render("  "+s.res.Dest)
	}
	return animBelow(m, text, s.rocket)
}
