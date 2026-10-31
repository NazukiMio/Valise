package tui

import (
	"fmt"
	"strings"

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

var modeNames = map[string]string{revive.ModeRollback: "回滚", revive.ModeMigrate: "迁移", revive.ModeClone: "克隆"}

func (r *reviveScreen) title() string { return "复活 " + r.snap.SnapshotID }
func (r *reviveScreen) help() string {
	switch r.step {
	case rvOptions:
		return "m 切换模式  ·  t 选择目标服务器  ·  d 允许未核实 profile  ·  s 允许快照内 profile  ·  enter 输入口令并出计划"
	case rvPlan:
		if len(r.plan.Blockers) > 0 {
			return "计划无法执行  ·  b 返回修改选项"
		}
		return "enter 暂存（活动目录不会被改动）  ·  b 返回修改选项"
	case rvAskSwap:
		return "y 开始交换  ·  n 放弃（暂存目录保留）"
	case rvSwapping:
		return "交换进行中，不能中断"
	}
	return ""
}
func (r *reviveScreen) init(m *Model) tea.Cmd { return nil }

// busy keeps the TUI open while a revive runs: quitting would cut the
// connection the staging or swap is using.
func (r *reviveScreen) busy() string {
	switch r.step {
	case rvStaging, rvAskSwap, rvSwapping:
		return "复活正在进行，不能退出；请等它结束（交换失败会自动退回）"
	}
	return ""
}

func (r *reviveScreen) update(m *Model, msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case planned:
		r.plan, r.err, r.step = msg.rp, msg.err, rvPlan
		if msg.err != nil {
			r.step = rvOptions
			return func() tea.Msg { return failed(msg.err) }, true
		}
		return nil, true
	case stageVerified:
		r.verify, r.reply, r.step = msg.v, msg.reply, rvAskSwap
		return nil, true
	case reviveFinished:
		r.result, r.err, r.step = msg.res, msg.err, rvDone
		if msg.err != nil && (msg.res == nil || msg.res.State == "") {
			return func() tea.Msg { return failed(msg.err) }, true
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
			order := []string{revive.ModeRollback, revive.ModeMigrate, revive.ModeClone}
			for i, mo := range order {
				if mo == r.opts.Mode {
					r.opts.Mode = order[(i+1)%len(order)]
					break
				}
			}
			if r.opts.Mode == revive.ModeRollback {
				r.opts.Target = r.from
			}
		case "t":
			hosts, err := m.svc.Servers()
			if err != nil {
				return func() tea.Msg { return failed(err) }, true
			}
			var cs []choice
			for _, h := range hosts {
				cs = append(cs, choice{label: h, value: h})
			}
			return m.push(pick("复活到哪台服务器", cs, func(m *Model, h string) tea.Cmd { r.opts.Target = h; return nil })), true
		case "d":
			r.opts.AllowDraft = !r.opts.AllowDraft
		case "s":
			r.opts.AllowSnapshotProfile = !r.opts.AllowSnapshotProfile
		case "enter":
			opts := r.opts
			return m.push(newInput("口令（只用于在本机解密这个快照，用完即弃）", "", true, func(m *Model, pass string) tea.Cmd {
				r.step = rvPlanning
				return func() tea.Msg {
					rp, err := m.svc.PlanRevive(m.ctx, opts, pass)
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
			r.step, r.plan = rvOptions, nil
		case "enter":
			if len(r.plan.Blockers) > 0 {
				return nil, true
			}
			rp := r.plan
			return m.push(confirm("按上述计划暂存？（活动目录在下一次确认前不会被改动）", nil, func(m *Model) tea.Cmd {
				r.step = rvStaging
				send := m.send
				// The revive keeps the root context: closing the screen must
				// not interrupt a swap.
				return func() tea.Msg {
					res, err := m.svc.ExecuteRevive(m.ctx, rp, "", func(v *revive.StageVerifyResult) bool {
						reply := make(chan bool, 1)
						send(stageVerified{v, reply})
						return <-reply
					})
					return reviveFinished{res, err}
				}
			})), true
		default:
			return nil, false
		}
		return nil, true
	case rvAskSwap:
		switch k {
		case "y":
			r.step = rvSwapping
			r.reply <- true
		case "n", "esc":
			r.step = rvStaging
			r.reply <- false
		}
		return nil, true
	case rvPlanning, rvStaging, rvSwapping:
		return nil, k == "esc" || k == "q" // stay until the operation reports back
	}
	return nil, false
}

func (r *reviveScreen) view(m *Model) string {
	var b strings.Builder
	fmt.Fprintf(&b, "快照 %s（%s 档，%s，实例 %s，来自 %s）\n", r.snap.SnapshotID, r.snap.Tier,
		r.snap.CreatedAt.Local().Format("2006-01-02 15:04"), r.snap.InstanceID, r.from)
	fmt.Fprintf(&b, "模式：%s  ·  目标服务器：%s", modeNames[r.opts.Mode], r.opts.Target)
	if r.opts.AllowDraft {
		b.WriteString("  ·  " + warnStyle.Render("允许未核实 profile"))
	}
	if r.opts.AllowSnapshotProfile {
		b.WriteString("  ·  " + warnStyle.Render("允许快照内 profile"))
	}
	b.WriteString("\n\n")
	switch r.step {
	case rvOptions:
		b.WriteString(dimStyle.Render("回滚：同一个实例退回这个快照。迁移、克隆：先按 t 选择另一台服务器。"))
	case rvPlanning:
		b.WriteString(dimStyle.Render("正在拉取金库、解密快照并检查目标服务器…"))
	case rvPlan:
		b.WriteString(planText(r.plan))
	case rvStaging:
		b.WriteString(dimStyle.Render("正在暂存并校验（活动目录还没有被改动）…"))
	case rvAskSwap:
		fmt.Fprintf(&b, "%s\n\n%s", okStyle.Render(fmt.Sprintf("暂存校验通过：%d 个文件，%s，SHA-256 与 SQLite 完整性检查全部通过。", r.verify.Files, size(r.verify.Bytes))),
			warnStyle.Render("开始交换？会停止服务；失败会自动退回。"))
	case rvSwapping:
		b.WriteString(warnStyle.Render("交换进行中（停止服务 → 交换 → 启动 → 健康检查），请勿关闭终端…"))
	case rvDone:
		b.WriteString(resultText(r.result, r.err))
	}
	return b.String()
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
	fmt.Fprintf(&b, "profile %s v%d（%s）\n", p.Profile.ID, p.Profile.ProfileVersion, origin)
	for _, root := range p.Roots {
		fmt.Fprintf(&b, "  目录 %-10s → %s\n", root.ID, root.Path)
	}
	secret := ""
	if rp.RestoreSecret {
		secret = "（含凭据）"
	}
	fmt.Fprintf(&b, "将恢复 %d 个文件，%s%s\n", rp.Files, size(rp.Bytes), secret)
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
	tuiHint := map[string]string{"profile_draft": "按 b 返回，按 d 允许未核实 profile 后重新出计划",
		"profile_from_snapshot": "按 b 返回，按 s 允许使用快照内的 profile 后重新出计划"}
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
		b.WriteString(okStyle.Render("复活完成，服务健康") + "\n")
	case revive.StateRolledBack:
		b.WriteString(errStyle.Render("rolled_back —— "+res.Message) + "\n")
		if res.Logs != "" {
			b.WriteString(dimStyle.Render(res.Logs) + "\n")
		}
	default:
		b.WriteString(res.Message + "\n")
	}
	for _, p := range res.Prev {
		fmt.Fprintf(&b, "保留交换前状态：%s（可在金库页面按 P 删除）\n", p)
	}
	for _, p := range res.Intruders {
		fmt.Fprintf(&b, "%s\n", warnStyle.Render("注意：交换期间有其他进程重新创建了活动目录，已挪到 "+p))
	}
	return b.String()
}
