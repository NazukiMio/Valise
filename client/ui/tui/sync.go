package tui

import (
	"time"

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
	m            *Model // for help(), which has no model
	ended        time.Time
}

type syncDone struct {
	res *vaultrpc.SyncResult
	err error
}

func newSync(host, target string) *syncScreen {
	return &syncScreen{host: host, target: target, running: true}
}

// startSync picks the location: none yet opens the cloud backup page, one
// starts at once, several are chosen from. A server without a name is
// named first (the name is its folder on the cloud).
func startSync(m *Model, host string) tea.Cmd {
	var names []string
	if st := m.server(host).status; st != nil && st.ServerName == "" {
		return nameServer(m, host, func(m *Model) tea.Cmd { return startSync(m, host) })
	}
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
	switch {
	case s.running:
		return ""
	case s.m != nil && !s.revealed(s.m):
		return menu("enter", "跳过动画")
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
	return s.rocket != nil
}

// revealed: the outcome may be shown (the rocket has left, was skipped,
// or there is no scene to wait for).
func (s *syncScreen) revealed(m *Model) bool {
	return s.err != nil || !hudFits(m.width, m.bodyHeight()) || s.rocket.done(m.now())
}

func (s *syncScreen) update(m *Model, msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case syncDone:
		s.running, s.res, s.err, s.ended = false, msg.res, msg.err, m.now()
		if s.err != nil {
			return nil, true // the rocket stays on the pad
		}
		// Uploads cannot be estimated yet: lift-off waits for the end.
		s.rocket.go_(m.now(), false)
		return m.loadServer(s.host), true
	case tea.KeyPressMsg:
		if s.running {
			return nil, msg.String() == "esc" || msg.String() == "q"
		}
		if msg.String() == "enter" && !s.revealed(m) {
			s.rocket.skip(m.now())
			return nil, true
		}
	}
	return nil, false
}

func (s *syncScreen) view(m *Model) string {
	s.m = m
	if hudFits(m.width, m.bodyHeight()) {
		return s.hudView(m)
	}
	head := headStyle.Render("上云：" + s.host + " → " + s.target)
	var text string
	switch {
	case s.running:
		text = head + "\n\n" + stepsView([]step{{"", "把还没上云的逃生舱传到 " + s.target}}, s.prog.get(), m.now(), s.rocket.start) +
			"\n" + dimStyle.Render("  传输由服务器直接发往云端，逃生舱一直是加密的；云端只追加，不改动已有的文件。")
	case s.err != nil:
		text = head + "\n\n" + errStyle.Render(errText(s.err))
	default:
		text = head + "\n\n" + s.resultText()
	}
	return text
}

func (s *syncScreen) resultText() string {
	return okStyle.Render("✓ 已上云") + "：" + itoa(len(s.res.Leaves)) + " 个周期、" + itoa(len(s.res.Snapshots)) + " 个逃生舱" +
		"\n" + dimStyle.Render("  "+s.res.Dest)
}

// hudView tells the upload as a launch (docs/UI.md section 8).
func (s *syncScreen) hudView(m *Model) string {
	now := m.now()
	route := s.host + " → " + s.target
	l := hudLayout{title: "发射遥测", captionStyle: hudBig,
		telemetry: [][2]string{{"已用时", clock(upTo(now, s.ended).Sub(s.rocket.start))}, {"目的地", s.target}}}
	switch {
	case s.running:
		l.caption, l.plain = "逃生舱等候发射…", "把还没上云的备份从 "+s.host+" 传到 "+s.target
		l.body = hudDim.Render("备份一直是加密的；传输由服务器直接发往云端，云端只追加，不改动已有的文件。")
		l.bottom = "上云  把还没上云的备份传到 " + s.target
		l.meter = hudMeter(s.prog.get(), m.width-16, now, false)
	case s.err != nil:
		l.caption, l.captionStyle, l.plain = "发射中止", hudBad, route
		l.body = errStyle.Render(errText(s.err))
	case !s.revealed(m):
		ph, _ := s.rocket.phase(now)
		l.caption, l.plain = "正在进入轨道…", route
		switch {
		case ph >= 17:
			l.caption = "空间站对接中…"
		case ph >= 12:
			l.caption = "正在呼叫空间站…"
		}
	default:
		l.caption, l.captionStyle = "逃生舱已接入空间站！", hudGood
		l.body = hudRows([][2]string{{"传送", itoa(len(s.res.Leaves)) + " 个周期、" + itoa(len(s.res.Snapshots)) + " 个逃生舱"}, {"位置", s.res.Dest}}, 46)
		l.telemetry[0][0] = "用时"
	}
	h := m.bodyHeight()
	return drawHUD(s.rocket.cells(now, m.width, h), m.width, h, l)
}
