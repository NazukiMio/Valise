package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"valise/core/revive"
	"valise/core/vault"
	"valise/ui/service"
)

// Wizard steps (plan → stage → confirm → swap → result).
const (
	rvOptions = iota
	rvPlanning
	rvPlan
	rvStaging
	rvAskSwap
	rvSwapping
	rvDone
)

// reviveScreen is the revive wizard for one snapshot.
type reviveScreen struct {
	from   string
	snap   vault.Meta
	opts   service.ReviveOptions
	step   int
	plan   *service.RevivePlan
	verify *revive.StageVerifyResult
	reply  chan bool // answers the swap confirmation of the running revive
	result *revive.SwapResult
	err    error
	// Progress of the running step and the landing scene (docs/UI.md 9.6).
	prog   progressBox
	began  time.Time // start of the running step (plain layout)
	t0     time.Time // start of the landing (telemetry)
	ended  time.Time
	rocket *rocketPlayer
	scroll int    // plan lines scrolled away
	m      *Model // for help(), which has no model
}

func newRevive(host string, snap vault.Meta) *reviveScreen {
	return &reviveScreen{from: host, snap: snap,
		opts: service.ReviveOptions{Target: host, From: host, SnapshotID: snap.SnapshotID, Mode: revive.ModeRollback, Roots: map[string]string{}}}
}

type planned struct {
	rp  *service.RevivePlan
	err error
}

// stageVerified comes from the running revive; the swap waits for reply.
type stageVerified struct {
	v     *revive.StageVerifyResult
	reply chan bool
}

type reviveFinished struct {
	res *revive.SwapResult
	err error
}

var modeNames = map[string]string{revive.ModeRollback: "原地着陆", revive.ModeMigrate: "着陆到另一台服务器", revive.ModeClone: "着陆成一个新的 agent"}

var modeChoices = []choice{
	{label: "原地着陆", value: revive.ModeRollback, desc: "让这个 agent 回到逃生舱建立的时刻；现在的样子会先保留下来"},
	{label: "着陆到另一台服务器", value: revive.ModeMigrate, desc: "把 agent 搬过去，原来的标记为\"已被取代\""},
	{label: "着陆成一个新的 agent", value: revive.ModeClone, desc: "在另一台服务器上复制一个新的，原来的不动"},
}

func (r *reviveScreen) title() string { return "逃生舱着陆" }
func (r *reviveScreen) help() string {
	switch r.step {
	case rvOptions:
		return menu("m", "着陆方式", "t", "目标服务器", "d", "允许着陆未测试的 harness 资料", "s", "允许快照里的 harness 资料", "enter", "输入口令并出计划")
	case rvPlan:
		if len(r.plan.Blockers) > 0 {
			return errStyle.Render("计划无法执行") + "   " + menu("b", "返回修改选项")
		}
		return menu("enter", "开始着陆（先暂存并校验）", "↑↓", "滚动计划", "b", "返回修改选项")
	case rvAskSwap:
		return menu("y", "下令着陆", "n", "放弃（暂存保留）")
	case rvSwapping:
		return warnStyle.Render("交换进行中，不能中断")
	case rvDone:
		if r.m != nil && !r.revealed(r.m) {
			return menu("enter", "跳过动画")
		}
	}
	return ""
}
func (r *reviveScreen) init(m *Model) tea.Cmd { return nil }

// busy keeps the TUI open while a revive runs: quitting would cut the
// connection the staging or swap is using.
func (r *reviveScreen) busy() string {
	switch r.step {
	case rvStaging, rvAskSwap, rvSwapping:
		return "着陆正在进行，不能退出；请等它结束（交换失败会自动退回原样）"
	}
	return ""
}

func (r *reviveScreen) update(m *Model, msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case planned:
		r.plan, r.err, r.step, r.scroll = msg.rp, msg.err, rvPlan, 0
		if msg.err != nil {
			r.step = rvOptions
			return func() tea.Msg { return failed(msg.err) }, true
		}
		return nil, true
	case stageVerified:
		r.verify, r.reply, r.step = msg.v, msg.reply, rvAskSwap
		return nil, true
	case reviveFinished:
		r.result, r.err, r.step, r.ended = msg.res, msg.err, rvDone, m.now()
		if r.rocket == nil {
			return nil, true
		}
		if r.landed() {
			// Landed: a hovering rocket touches down; the outcome shows
			// once it has (docs/UI.md section 8).
			r.rocket.go_(m.now(), false)
			r.rocket.release(m.now())
		} else {
			r.rocket.fail(m.now())
		}
		return nil, true
	case tea.KeyPressMsg:
		return r.key(m, msg.String())
	}
	return nil, false
}

func (r *reviveScreen) key(m *Model, k string) (tea.Cmd, bool) {
	switch r.step {
	case rvOptions:
		switch k {
		case "m":
			return m.push(pick("着陆到哪里？", modeChoices, func(m *Model, mode string) tea.Cmd {
				r.opts.Mode = mode
				if mode == revive.ModeRollback {
					r.opts.Target = r.from
				}
				return nil
			})), true
		case "t":
			hosts, err := m.svc.Servers()
			if err != nil {
				return func() tea.Msg { return failed(err) }, true
			}
			var cs []choice
			for _, h := range hosts {
				cs = append(cs, choice{label: h, value: h})
			}
			return m.push(pick("着陆到哪台服务器", cs, func(m *Model, h string) tea.Cmd { r.opts.Target = h; return nil })), true
		case "d":
			r.opts.AllowDraft = !r.opts.AllowDraft
		case "s":
			r.opts.AllowSnapshotProfile = !r.opts.AllowSnapshotProfile
		case "enter":
			opts := r.opts
			return m.push(newInput("口令（只在这台电脑上用来打开逃生舱，用完即弃）", "", true, func(m *Model, pass string) tea.Cmd {
				r.step = rvPlanning
				r.startScene(m)
				ctx := service.WithProgress(m.ctx, r.prog.set)
				return func() tea.Msg {
					rp, err := m.svc.PlanRevive(ctx, opts, pass)
					return planned{rp, err}
				}
			})), true
		default:
			return nil, false
		}
		return nil, true
	case rvPlan:
		switch k {
		case "b":
			r.plan.Close()
			r.step, r.plan = rvOptions, nil
		case "enter":
			if len(r.plan.Blockers) > 0 {
				return nil, true
			}
			// The plan on screen says nothing changes before the swap is
			// confirmed; enter starts staging.
			rp := r.plan
			r.step = rvStaging
			r.startScene(m)
			send := m.send
			// The revive keeps the root context: closing the screen must
			// not interrupt a swap.
			ctx := service.WithProgress(m.ctx, r.prog.set)
			return func() tea.Msg {
				res, err := m.svc.ExecuteRevive(ctx, rp, "", func(v *revive.StageVerifyResult) bool {
					reply := make(chan bool, 1)
					send(stageVerified{v, reply})
					return <-reply
				})
				rp.Close()
				return reviveFinished{res, err}
			}, true
		case "up", "k":
			r.scroll--
		case "down", "j":
			r.scroll++
		default:
			return nil, false
		}
		return nil, true
	case rvAskSwap:
		switch k {
		case "y":
			r.step = rvSwapping
			r.began = m.now()
			r.prog.set(service.Progress{Stage: service.StageSwap})
			if r.rocket != nil {
				// The swap and health check take seconds: the rocket comes
				// down now and hovers if they are not done yet.
				r.rocket.go_(m.now(), true)
			}
			r.reply <- true
		case "n", "esc":
			r.step = rvStaging
			r.reply <- false
		}
		return nil, true
	case rvPlanning, rvStaging, rvSwapping:
		return nil, k == "esc" || k == "q" // stay until the operation reports back
	case rvDone:
		if k == "enter" && !r.revealed(m) {
			r.rocket.skip(m.now())
			return nil, true
		}
	}
	return nil, false
}

func (r *reviveScreen) view(m *Model) string {
	r.m = m
	if r.step != rvOptions && r.rocket != nil && hudFits(m.width, m.bodyHeight()) {
		return r.hudView(m)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "逃生舱：%s 的 %s · %s · 来自 %s\n", r.snap.InstanceID,
		r.snap.CreatedAt.Local().Format("2006-01-02 15:04"), tierNames[r.snap.Tier], r.from)
	fmt.Fprintf(&b, "着陆方式：%s  ·  目标服务器：%s", accStyle.Render(modeNames[r.opts.Mode]), r.opts.Target)
	if r.opts.AllowDraft {
		b.WriteString("  ·  " + warnStyle.Render("允许着陆未测试的 harness 资料"))
	}
	if r.opts.AllowSnapshotProfile {
		b.WriteString("  ·  " + warnStyle.Render("允许快照里的 harness 资料"))
	}
	b.WriteString("\n\n")
	switch r.step {
	case rvOptions:
		b.WriteString(dimStyle.Render("接下来：输入口令 → 看计划 → 暂存并校验 → 你确认 → 交换 → 健康检查。\n着陆到另一台服务器或着陆成新的 agent 时，先按 t 选择目标服务器。"))
	case rvPlanning:
		b.WriteString(dimStyle.Render("出计划：只读，什么都不会改动。") + "\n\n" + stepsView(planSteps, r.prog.get(), m.now(), r.began))
	case rvPlan:
		b.WriteString(planText(r.plan))
	case rvStaging:
		b.WriteString(dimStyle.Render("agent 现在的文件还没有被改动。") + "\n\n" + stepsView(stageSteps, r.prog.get(), m.now(), r.began))
	case rvAskSwap:
		fmt.Fprintf(&b, "%s\n\n%s", okStyle.Render(fmt.Sprintf("暂存与校验都通过了：%d 个文件，%s，SHA-256 与 SQLite 完整性检查全部通过。", r.verify.Files, size(r.verify.Bytes))),
			warnStyle.Render("开始交换？会停止服务；失败会自动退回。"))
	case rvSwapping:
		b.WriteString(warnStyle.Render("交换进行中，请勿关闭终端。") + "\n\n" + stepsView(swapSteps, r.prog.get(), m.now(), r.began))
	case rvDone:
		b.WriteString(resultText(r.result, r.err))
	}
	return b.String()
}

// Steps shown while the revive runs.
var (
	planSteps = []step{{service.StagePullMeta, "取回周期的索引与清单（很小）"}, {service.StageOpen, "在这台电脑上打开（解密）"},
		{service.StagePullPacks, "只取回这个逃生舱用到的数据包（已有的核对后复用）"},
		{service.StageInspect, "检查目标服务器"}}
	stageSteps = []step{{service.StageStage, "写入目标服务器的暂存目录"}, {service.StageVerify, "校验暂存的文件（SHA-256、SQLite）"}}
	swapSteps  = []step{{service.StageSwap, "停止服务 → 交换 → 启动 → 健康检查"}}
)

// startScene resets the progress and starts the landing scene: stars and
// the moon while the work runs, the rocket only once it has landed.
func (r *reviveScreen) startScene(m *Model) {
	r.prog.set(service.Progress{})
	r.began = m.now()
	if r.t0.IsZero() {
		r.t0 = m.now()
	}
	if r.rocket == nil {
		r.rocket = newRocket(sceneLanding, m.now())
	}
}

func (r *reviveScreen) animating(m *Model) bool {
	return r.step != rvOptions
}

func planText(rp *service.RevivePlan) string {
	var b strings.Builder
	p := rp.Plan
	origin := "官方"
	switch {
	case rp.ProfileFromSnapshot:
		origin = "来自快照，未经当前客户端审核"
	case rp.ProfileLocal:
		origin = "本地，未经审核"
	}
	fmt.Fprintf(&b, "操作 %s：%s，目标 %s（%s）上的实例 %s\n", p.OpID, modeNames[p.Mode], rp.Target, rp.TargetArch, p.InstanceID)
	if l := rp.Transfer.Summary(); l != "" {
		b.WriteString(l + "\n")
	}
	fmt.Fprintf(&b, "harness 资料 %s v%d（%s）\n", p.Profile.ID, p.Profile.ProfileVersion, origin)
	for _, root := range p.Roots {
		fmt.Fprintf(&b, "  目录 %-10s → %s\n", root.ID, root.Path)
	}
	secret := ""
	if rp.RestoreSecret {
		secret = "（含凭据）"
	}
	fmt.Fprintf(&b, "将放回 %d 个文件，%s%s\n", rp.Files, size(rp.Bytes), secret)
	for _, e := range rp.Excluded {
		fmt.Fprintf(&b, "  不恢复独占凭据：%s\n", e)
	}
	switch p.Body.Action {
	case revive.BodyKeep:
		b.WriteString("身体：目标上已有同一来源、同一版本，保持不变\n")
	case revive.BodyMoveDirs:
		fmt.Fprintf(&b, "身体：保持不变（交换时搬动 %v）\n", p.Body.Dirs)
	case revive.BodyNPMInstall:
		fmt.Fprintf(&b, "身体：按来源记录安装 %s@%s\n", p.Body.Package, p.Body.Version)
	}
	if p.Unit != "" {
		fmt.Fprintf(&b, "服务：%s", p.Unit)
		if p.InstallService {
			fmt.Fprintf(&b, "（目标上没有，将执行 harness 自带的安装命令：%s）", strings.Join(rp.InstallArgv, " "))
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "预计停机：%s\n空间：需要约 %s，目标可用 %s\n", rp.Downtime, size(rp.NeedBytes), size(int64(rp.FreeBytes)))
	for _, w := range rp.Warnings {
		b.WriteString(warnStyle.Render("· "+w) + "\n")
	}
	tuiHint := map[string]string{"profile_draft": "按 b 返回，按 d 允许着陆未测试的 harness 资料后重新出计划",
		"profile_from_snapshot": "按 b 返回，按 s 允许使用逃生舱里保存的 harness 资料后重新出计划"}
	for _, bl := range rp.Blockers {
		hint := bl.Suggestion
		if h, ok := tuiHint[bl.Code]; ok {
			hint = h
		}
		fmt.Fprintf(&b, "%s\n  建议：%s\n", errStyle.Render("无法执行："+bl.Reason), hint)
	}
	return b.String()
}

func resultText(res *revive.SwapResult, err error) string {
	if res == nil || res.State == "" {
		return errStyle.Render(errText(err))
	}
	var b strings.Builder
	switch res.State {
	case revive.StateHealthy:
		b.WriteString(okStyle.Render("着陆完成，服务健康") + "\n")
	case revive.StateRolledBack:
		b.WriteString(errStyle.Render("着陆没有成功，已自动退回原样："+res.Message) + "\n")
		if res.Logs != "" {
			b.WriteString(dimStyle.Render(res.Logs) + "\n")
		}
	default:
		b.WriteString(okStyle.Render("着陆完成") + "：" + res.Message + "\n")
	}
	for _, p := range res.Prev {
		fmt.Fprintf(&b, "着陆前的样子保留在 %s（不需要时在 服务器 › 空间清理 中删除）\n", p)
	}
	for _, p := range res.Intruders {
		fmt.Fprintf(&b, "%s\n", warnStyle.Render("注意：交换期间有其他进程重新创建了活动目录，已挪到 "+p))
	}
	return b.String()
}
