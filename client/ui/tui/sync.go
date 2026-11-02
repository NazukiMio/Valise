package tui

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"valise/ui/service"
)

// syncScreen sends a server's escape pods to repositories (local folders)
// and stations (append-only: only what a place lacks is copied). The
// launch scene waits on the pad while it runs and lifts off when it
// succeeds.
type syncScreen struct {
	host    string
	places  []place
	running bool
	results []string // one line per place
	err     error
	rocket  *rocketPlayer
	prog    progressBox
	m       *Model // for help(), which has no model
	ended   time.Time
}

// place is a repository or a station.
type place struct {
	repo bool
	name string
}

func (p place) label() string {
	if p.repo {
		return "仓库 " + p.name
	}
	return "空间站 " + p.name
}

type syncDone struct {
	results []string
	err     error
}

func newSync(host string, places ...place) *syncScreen {
	return &syncScreen{host: host, places: places, running: true}
}

// places lists host's repositories, then its stations.
func places(m *Model, host string) []place {
	var out []place
	for _, r := range m.svc.Repos() {
		out = append(out, place{repo: true, name: r.Name})
	}
	if st := m.server(host).status; st != nil {
		for _, t := range st.Targets {
			out = append(out, place{name: t.Name})
		}
	}
	return out
}

// startSync picks the place: none yet opens the repositories and stations
// page, one starts at once, several are chosen from (or all). A server
// without a name is named first (the name is its folder).
func startSync(m *Model, host string) tea.Cmd {
	if st := m.server(host).status; st != nil && st.ServerName == "" {
		return nameServer(m, host, func(m *Model) tea.Cmd { return startSync(m, host) })
	}
	ps := places(m, host)
	switch len(ps) {
	case 0:
		m.flash, m.flashErr = "还没有仓库或空间站；先在这里添加一个（按 +）。", false
		return m.push(newCloud(host))
	case 1:
		return m.push(newSync(host, ps[0]))
	}
	cs := []choice{{label: "全部", value: "*", desc: itoa(len(ps)) + " 处，依次进行"}}
	for i, p := range ps {
		cs = append(cs, choice{label: p.label(), value: itoa(i)})
	}
	return m.push(pick("上云到哪里？", cs, func(m *Model, v string) tea.Cmd {
		if v == "*" {
			return m.push(newSync(host, ps...))
		}
		for i, p := range ps {
			if itoa(i) == v {
				return m.push(newSync(host, p))
			}
		}
		return nil
	}))
}

func (s *syncScreen) title() string    { return "上云" }
func (s *syncScreen) hostName() string { return s.host }
func (s *syncScreen) help() string {
	if !s.running && s.m != nil && !s.revealed(s.m) {
		return menu("enter", "跳过动画")
	}
	return ""
}
func (s *syncScreen) busy() string {
	if s.running {
		return "正在上云，等它结束；中途断开不会损坏任何东西，但这次要重新开始"
	}
	return ""
}

func (s *syncScreen) init(m *Model) tea.Cmd {
	s.rocket = newRocket(sceneLaunch, m.now())
	svc, host, places := m.svc, s.host, s.places
	ctx := service.WithProgress(m.ctx, s.prog.set)
	return func() tea.Msg {
		var lines []string
		for _, p := range places {
			line, err := syncPlace(ctx, svc, host, p)
			if err != nil {
				return syncDone{lines, err}
			}
			lines = append(lines, line)
		}
		return syncDone{lines, nil}
	}
}

// syncPlace copies to one place and describes the result.
func syncPlace(ctx context.Context, svc *service.Service, host string, p place) (string, error) {
	if p.repo {
		r, err := svc.RepoSync(ctx, host, p.name)
		if err != nil {
			return "", err
		}
		return p.label() + "：" + itoa(len(r.Snapshots)) + " 个逃生舱，新复制 " + size(r.Bytes) + "，校验通过 · " + r.Dir, nil
	}
	r, err := svc.Sync(ctx, host, p.name)
	if err != nil {
		return "", err
	}
	line := p.label() + "：" + itoa(len(r.Leaves)) + " 个周期、" + itoa(len(r.Snapshots)) + " 个逃生舱 · " + r.Dest
	if r.Note != "" {
		line += "\n  " + r.Note
	}
	return line, nil
}

func (s *syncScreen) animating(m *Model) bool { return s.rocket != nil }

// revealed: the outcome may be shown (the rocket has left, was skipped,
// or there is no scene to wait for).
func (s *syncScreen) revealed(m *Model) bool {
	return s.err != nil || !hudFits(m.width, m.bodyHeight()) || s.rocket.done(m.now())
}

func (s *syncScreen) update(m *Model, msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case syncDone:
		s.running, s.results, s.err, s.ended = false, msg.results, msg.err, m.now()
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

func (s *syncScreen) dest() string {
	var names []string
	for _, p := range s.places {
		names = append(names, p.label())
	}
	return strings.Join(names, "、")
}

func (s *syncScreen) view(m *Model) string {
	s.m = m
	if hudFits(m.width, m.bodyHeight()) {
		return s.hudView(m)
	}
	head := headStyle.Render("上云：" + s.host + " → " + s.dest())
	switch {
	case s.running:
		return head + "\n\n" + stepsView([]step{{"", "把还没上云的逃生舱复制过去"}}, s.prog.get(), m.now(), s.rocket.start) +
			"\n" + dimStyle.Render("  逃生舱一直是加密的；只追加，不改动已有的文件。")
	case s.err != nil:
		return head + "\n\n" + strings.Join(s.results, "\n") + "\n" + errStyle.Render(errText(s.err))
	}
	return head + "\n\n" + okStyle.Render("✓ 已上云") + "\n" + strings.Join(s.results, "\n")
}

// hudView tells the upload as a launch (docs/UI.md section 8).
func (s *syncScreen) hudView(m *Model) string {
	now := m.now()
	route := s.host + " → " + s.dest()
	l := hudLayout{title: "发射遥测", captionStyle: hudBig,
		telemetry: [][2]string{{"已用时", clock(upTo(now, s.ended).Sub(s.rocket.start))}, {"目的地", s.dest()}}}
	switch {
	case s.running:
		l.caption, l.plain = "逃生舱等候发射…", "把还没上云的备份从 "+s.host+" 复制到 "+s.dest()
		l.body = hudDim.Render("备份一直是加密的；只追加，不改动已有的文件。")
		l.bottom = "上云  " + s.dest()
		p := s.prog.get()
		l.meter = hudMeter(p, m.width-16, now, false)
		if p.Total > 0 {
			l.amount = size(min(p.Done, p.Total)) + " / " + size(p.Total)
		}
	case s.err != nil:
		l.caption, l.captionStyle, l.plain = "发射中止", hudBad, route
		l.body = strings.Join(append(s.results, errStyle.Render(errText(s.err))), "\n")
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
		l.body = strings.Join(s.results, "\n")
		l.telemetry[0][0] = "用时"
	}
	h := m.bodyHeight()
	return drawHUD(s.rocket.cells(now, m.width, h), m.width, h, l)
}
