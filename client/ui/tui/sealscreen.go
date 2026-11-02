package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"valise/core/vaultrpc"
	"valise/ui/service"
)

// sealScreen builds an escape pod with the assembly scene above and the
// stages below (docs/UI.md 9.5 "建立逃生舱的进度"). esc leaves it running:
// the result then reaches the page underneath as a sealDone.
type sealScreen struct {
	host, inst, tier string
	prog             progressBox
	began, ended     time.Time
	scene            *assemblyPlayer
	done             *sealDone
}

func newSealScreen(m *Model, host, inst, tier string) *sealScreen {
	return &sealScreen{host: host, inst: inst, tier: tier, began: m.now(), scene: newAssembly(m.now())}
}

var sealSteps = []step{{service.StageSealScan, "清点文件"}, {service.StageSealPack, "读取、加密并写入"}, {service.StageSealCommit, "写入逃生舱目录"}}

func (s *sealScreen) title() string    { return "建立逃生舱" }
func (s *sealScreen) hostName() string { return s.host }
func (s *sealScreen) help() string {
	if s.done == nil {
		return menu("esc", "在后台继续")
	}
	return menu("enter", "完成")
}

// run starts the seal; reason is what the user typed.
func (s *sealScreen) run(m *Model, reason string) tea.Cmd {
	svc, host := m.svc, s.host
	ctx := service.WithProgress(m.ctx, s.prog.set)
	params := vaultrpc.SealParams{InstanceID: s.inst, Tier: s.tier, Reason: strings.TrimSpace(reason)}
	return func() tea.Msg {
		rep, err := svc.Seal(ctx, host, params)
		return sealDone{rep, err}
	}
}

func (s *sealScreen) init(m *Model) tea.Cmd { return nil }

func (s *sealScreen) animating(m *Model) bool {
	return s.done == nil || !s.scene.over(m.now())
}

func (s *sealScreen) update(m *Model, msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case sealDone:
		s.done, s.ended = &msg, m.now()
		s.scene.finish(m.now())
		return nil, true
	case tea.KeyPressMsg:
		switch k := msg.String(); {
		case s.done == nil && (k == "esc" || k == "q"):
			m.pop()
			m.flash, m.flashErr = "逃生舱在后台继续建立，完成后这里会提示", false
			return nil, true
		case s.done != nil && (k == "enter" || k == "esc" || k == "q"):
			d := *s.done
			m.pop()
			return func() tea.Msg { return d }, true
		}
		return nil, true
	}
	return nil, false
}

func (s *sealScreen) view(m *Model) string {
	now := m.now()
	h := m.bodyHeight()
	if !hudFits(m.width, h) {
		return s.plainView(now)
	}
	l := hudLayout{title: "建立逃生舱 · " + tierNames[s.tier], captionStyle: hudBig}
	l.telemetry = [][2]string{{"已用时", clock(upTo(now, s.ended).Sub(s.began))}, {"agent", s.inst + " · " + s.host}}
	p := s.prog.get()
	cur := 0
	for i, st := range sealSteps {
		if st.stage == p.Stage {
			cur = i
		}
	}
	switch {
	case s.done != nil && s.done.err != nil:
		l.caption, l.captionStyle = "逃生舱没有建成", hudBad
		l.body = errText(s.done.err) + "\n\n" + hudDim.Render("agent 没有受到影响。")
	case s.done != nil:
		r := s.done.rep
		l.caption, l.captionStyle = "逃生舱建好了", hudGood
		l.body = hudRows([][2]string{{"档位", tierNames[r.Tier]}, {"文件", itoa(r.Files) + " 个 · " + size(r.PlainBytes)},
			{"新写入", size(r.AddedBytes) + "（和以前相同的内容不重复存）"}, {"存在", "服务器 " + s.host}}, 46)
		l.telemetry[0][0] = "用时"
	default:
		l.caption, l.plain = "正在打包 agent 现在的样子", "agent 照常运行，不会被停下或改动"
		l.body = hudSteps(stepNames(sealSteps), cur, now)
		l.bottom = sealSteps[cur].label
		l.meter = hudMeter(p, m.width-16, now, false)
		if p.Total > 0 {
			l.amount = size(min(p.Done, p.Total)) + " / " + size(p.Total)
			if speed, left := eta(p, s.prog.stageTime()); speed != "" {
				l.telemetry = append(l.telemetry[:1], [2]string{"预计剩余", left}, [2]string{"速度", speed}, l.telemetry[1])
			}
		}
	}
	return drawHUD(s.scene.cells(now, m.width, h), m.width, h, l)
}

// plainView is the screen without the scene, for small terminals.
func (s *sealScreen) plainView(now time.Time) string {
	switch {
	case s.done != nil && s.done.err != nil:
		return errStyle.Render("逃生舱没有建成") + "\n\n" + errText(s.done.err)
	case s.done != nil:
		r := s.done.rep
		return okStyle.Render("逃生舱建好了") + "\n\n" + kv("档位", tierNames[r.Tier], "文件", itoa(r.Files)+" 个 · "+size(r.PlainBytes), "新写入", size(r.AddedBytes))
	}
	return dimStyle.Render("agent 照常运行，不会被停下或改动。") + "\n\n" + stepsView(sealSteps, s.prog.get(), now, s.began)
}

func stepNames(steps []step) []string {
	out := make([]string, len(steps))
	for i, s := range steps {
		out[i] = s.label
	}
	return out
}
