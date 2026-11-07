package tui

import (
	"fmt"
	"strings"

	"valise/core/revive"
	"valise/ui/service"
)

// The landing told as a flight (docs/UI.md section 8): themed steps, each
// with a plain line saying what really happens.
var (
	landNames    = []string{"进入预备轨道", "进入轨道", "联络母星", "呼叫地面指挥", "登陆"}
	landCaptions = []string{"正在进入预备轨道…", "正在进入轨道…", "正在联络母星…", "正在呼叫地面指挥…", "即将完成…"}
	landPlain    = []string{"将备份取回本机", "解密备份，检查里面的文件和目标服务器", "把备份写入服务器的暂存目录（agent 现在的文件不会被改动）",
		"核对暂存的文件（SHA-256、SQLite）", "把 agent 换成备份里的版本，并做健康检查"}
)

// landStep is the themed step of the running revive.
func (r *reviveScreen) landStep() int {
	stage := r.prog.get().Stage
	switch r.step {
	case rvPlanning:
		if stage == service.StageOpen || stage == service.StageInspect {
			return 1
		}
		return 0
	case rvStaging:
		if stage == service.StageVerify {
			return 3
		}
		return 2
	case rvAskSwap:
		return 3
	}
	return 4
}

// revealed: the outcome may be shown (the rocket has finished, was
// skipped, or there is no scene to wait for).
func (r *reviveScreen) revealed(m *Model) bool {
	return r.rocket == nil || !hudFits(m.width, m.bodyHeight()) || r.rocket.done(m.now()) || !r.landed()
}

// landed: the revive finished with the agent in place.
func (r *reviveScreen) landed() bool {
	return r.result != nil && r.result.State != "" && r.result.State != revive.StateRolledBack
}

func (r *reviveScreen) hudView(m *Model) string {
	now := m.now()
	l := hudLayout{title: "恢复遥测", captionStyle: hudBig}
	target := r.opts.Target + " · " + modeNames[r.opts.Mode]
	l.telemetry = [][2]string{{"已用时", clock(upTo(now, r.ended).Sub(r.t0))}, {"目标", target}}
	cur := r.landStep()
	p := r.prog.get()
	meterW := m.width - 16
	switch r.step {
	case rvPlan:
		l.caption, l.body = "轨道就绪 · 请确认恢复计划", r.planBody()
		if len(r.plan.Blockers) > 0 {
			l.caption, l.captionStyle = "计划无法执行", hudBad
		}
	case rvDone:
		switch {
		case r.landed() && !r.revealed(m):
			l.caption, l.plain = landCaptions[4], landPlain[4]
			l.body = hudSteps(landNames, 4, now)
		case r.landed():
			l.caption, l.captionStyle = "恢复成功！", hudGood
			l.body = r.hudResult()
			l.telemetry[0][0] = "用时"
		default:
			l.caption, l.captionStyle = "恢复中止", hudBad
			l.body = strings.TrimRight(resultText(r.result, r.err), "\n")
			l.telemetry[0][0] = "用时"
		}
	default:
		l.caption, l.plain = landCaptions[cur], landPlain[cur]
		l.body = hudSteps(landNames, cur, now)
		l.bottom = "第 " + itoa(cur+1) + "/5 步  " + landNames[cur]
		still := r.step == rvAskSwap
		l.meter = hudMeter(p, meterW, now, still)
		if p.Total > 0 && r.step != rvAskSwap {
			l.amount = size(min(p.Done, p.Total)) + " / " + size(p.Total)
			if speed, left := eta(p, r.prog.stageTime()); speed != "" {
				l.telemetry = append(l.telemetry[:1], [2]string{"预计剩余", left}, [2]string{"速度", speed}, l.telemetry[1])
			}
		}
		if r.step == rvAskSwap {
			l.plain = "暂存的文件已核对，等你下令"
			l.bottom = "第 4/5 步  等待你的指令"
			l.body += "\n\n" + okStyle.Render(fmt.Sprintf("暂存与校验都通过了：%d 个文件，%s。", r.verify.Files, size(r.verify.Bytes))) +
				"\n" + warnStyle.Bold(true).Render("把 agent 换成备份里的版本？") + hudDim.Render("会停止服务；失败会自动退回。") +
				"\n\n" + menu("y", "确认恢复", "n", "放弃（暂存保留）")
			l.telemetry = append(l.telemetry, [2]string{"暂存", size(r.verify.Bytes) + " ✓"})
		}
		if r.step == rvSwapping {
			l.telemetry = append(l.telemetry[:1], [2]string{"预计剩余", "几秒"}, l.telemetry[1])
		}
	}
	h := m.bodyHeight()
	return drawHUD(r.rocket.cells(now, m.width, h), m.width, h, l)
}

// planBody is the plan with the keys to go on, scrolled by ↑↓.
func (r *reviveScreen) planBody() string {
	lines := strings.Split(strings.TrimRight(planText(r.plan), "\n"), "\n")
	r.scroll = max(0, min(r.scroll, len(lines)-1))
	body := strings.Join(lines[r.scroll:], "\n")
	if r.scroll > 0 {
		body = hudFaint.Render("↑ 上面还有") + "\n" + body
	}
	keys := menu("enter", "开始恢复（先暂存并校验，agent 现在的文件不会被改动）", "b", "返回修改选项")
	if len(r.plan.Blockers) > 0 {
		keys = menu("b", "返回修改选项")
	}
	return keys + "\n\n" + body
}

// hudResult is a finished landing.
func (r *reviveScreen) hudResult() string {
	health := "通过"
	if r.result.State != revive.StateHealthy {
		health = r.result.Message
	}
	out := []string{hudRows([][2]string{{"实例", r.snap.InstanceID}, {"服务器", r.opts.Target}, {"健康检查", health}}, 46)}
	for _, p := range r.result.Prev {
		out = append(out, "", hudDim.Render("恢复前的样子保留在 "+p+"（不需要时在 服务器 › 空间清理 中删除）"))
	}
	for _, p := range r.result.Intruders {
		out = append(out, warnStyle.Render("注意：交换期间有其他进程重新创建了活动目录，已挪到 "+p))
	}
	return strings.Join(out, "\n")
}
