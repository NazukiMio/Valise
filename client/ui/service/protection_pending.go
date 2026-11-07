package service

import (
	"context"
	"strconv"

	"valise/client/ssh"
	"valise/core/identity"
	"valise/core/protection"
	"valise/core/valise"
)

// pendingPlanItem reports a plan change a crash or a failed check left
// unsettled. Planned backups stay paused until the person settles it.
func pendingPlanItem(host, instance string, pc protection.PendingChange) HealthItem {
	it := HealthItem{ID: "protection-pending", Group: HealthAgent, Level: HealthBad, Title: "保护设置",
		Text: "有一次修改没有确认完成，按计划备份已暂停"}
	cmd := "valise protection resolve " + ssh.Quote(host) + " " + ssh.Quote(instance) + " "
	if pc.Committed {
		it.Detail = "新设置（版本 " + strconv.FormatUint(uint64(pc.NewRevision), 10) + "）已经写入，但还没确认定时器按它生效。核实后会恢复按计划备份：" + cmd + "--verify"
	} else {
		it.Detail = "修改中途中断，服务器上可能还是旧设置（版本 " + strconv.FormatUint(uint64(pc.OldRevision), 10) + "）。可以核实后完成，或放弃这次修改恢复旧设置：" +
			cmd + "--verify 或 " + cmd + "--rollback"
	}
	return it
}

// ResolveProtection settles the instance's unsettled plan change with one
// fixed, explicit choice: verify (finish if the server shows the new
// settings in effect) or rollback (restore the old settings; only before the
// new plan was published). It never repeats an unknown systemctl step.
func (s *Service) ResolveProtection(ctx context.Context, host, instance, action string) (*protection.Change, error) {
	if action != "verify" && action != "rollback" {
		return nil, valise.New("request_invalid", "需要明确选择核实或放弃", "加 --verify 或 --rollback")
	}
	var out *protection.Change
	err := s.withSession(ctx, host, func(c caller) error {
		var b identity.Binding
		if err := c.Call(ctx, identity.MethodGet, identity.GetParams{InstanceID: instance}, &b); err != nil {
			return err
		}
		if b.IdentityState != identity.Bound || b.Agent == nil {
			return valise.New("identity_unbound", "这个实例还没有身份，不会有保护计划的修改", "先接入实例")
		}
		var v protection.View
		if err := c.Call(ctx, protection.MethodGet, protection.GetParams{AgentRef: b.Agent.Ref}, &v); err != nil {
			return err
		}
		if len(v.PendingChanges) == 0 {
			return valise.New("nothing_pending", "保护设置没有待确认的修改", "")
		}
		pc := v.PendingChanges[0]
		if pc.Committed && action == "rollback" {
			return valise.New("rollback_after_publish", "新设置已经写入，不能放弃这次修改", "用 --verify 核实；要回到旧设置，核实后再提交一次修改")
		}
		var ch protection.Change
		if err := c.Call(ctx, protection.MethodReconcile, protection.ReconcileParams{AgentRef: b.Agent.Ref, ChangeID: pc.ChangeID, Action: action}, &ch); err != nil {
			return err
		}
		out = &ch
		return nil
	})
	return out, err
}
