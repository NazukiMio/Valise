package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"valise/core/revive"
	"valise/ui/service"
)

// sourcePick asks what to do with a snapshot whose source generation cannot
// be verified (contracts 16.D), reusing the plain choice list.
func sourcePick(r *reviveScreen) *pickScreen {
	us := r.plan.UnverifiedSource
	cs := []choice{{label: "我清楚，继续", value: string(service.SourceProceed), desc: "按上面列出的快照信息自行判断后恢复"}}
	if us.BackupFirst {
		cs = append(cs, choice{label: "先备份当前状态再恢复", value: string(service.SourceBackupFirst),
			desc: "先给目标上的 " + us.BackupInstance + " 做一次完整备份，成功后才开始"})
	}
	cs = append(cs, choice{label: "取消", value: string(service.SourceCancel), desc: "不做任何改动"})
	return pick("这个快照的来源无法核实", cs, func(m *Model, v string) tea.Cmd {
		if v == string(service.SourceCancel) {
			return nil
		}
		return r.stage(m, service.SourceDecision(v))
	})
}

// stage starts staging and, after the stage check, asks the swap question.
// The revive keeps the root context: closing the screen must not interrupt
// a swap.
func (r *reviveScreen) stage(m *Model, decision service.SourceDecision) tea.Cmd {
	rp := r.plan
	r.step = rvStaging
	r.startScene(m)
	send := m.send
	ctx := service.WithProgress(m.ctx, r.prog.set)
	return func() tea.Msg {
		res, err := m.svc.ExecuteReviveConfirmed(ctx, rp, "", service.ReviveConfirm{
			UnverifiedSource: func(service.UnverifiedSource) service.SourceDecision { return decision },
			Swap: func(v *revive.StageVerifyResult) bool {
				reply := make(chan bool, 1)
				send(stageVerified{v, reply})
				return <-reply
			},
		})
		rp.Close()
		return reviveFinished{res, err}
	}
}

// unverifiedText lists what the snapshot itself records, for the person to
// judge; none of it is evidence of where it came from.
func unverifiedText(us *service.UnverifiedSource) string {
	if us == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s：快照记录的实例 %s", us.Notice, us.InstanceID)
	if us.Name != "" {
		fmt.Fprintf(&b, "（%s）", us.Name)
	}
	fmt.Fprintf(&b, "，资料 %s v%d，来自 %s 的 %s 组，%s，%s\n",
		us.ProfileID, us.ProfileVersion, us.SourceServer, us.Period, us.CreatedAt.Local().Format("2006-01-02 15:04"), us.Tier)
	return b.String()
}
