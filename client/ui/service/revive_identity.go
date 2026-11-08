package service

import (
	"context"
	"slices"

	"valise/core/identity"
	"valise/core/protection"
	"valise/core/revive"
	"valise/core/valise"
)

// prepareRecoveryIdentity reads capabilities and exact snapshot/run evidence.
// A current instance with the same name is never evidence for an old snapshot.
// rp.sourceAgent is set only from a bound backup_snapshot_binding result, also
// when the target cannot record relations, so the plan can say whether the
// snapshot's origin is verifiable at all.
func (s *Service) prepareRecoveryIdentity(ctx context.Context, rp *RevivePlan, plan *revive.Plan) error {
	target, err := s.session(ctx, rp.Target)
	if err != nil {
		return err
	}
	supported := target.hello != nil && slices.Contains(target.hello.Capabilities, identity.Capability)
	lostOK := target.hello != nil && slices.Contains(target.hello.Capabilities, identity.LostSourceCapability)
	if target.hello != nil {
		rp.targetCaps = target.hello.Capabilities
	}
	target.Close()
	if supported && rp.SourceLost && plan.Mode == revive.ModeMigrate && !lostOK {
		return valise.New("unsupported_capability", rp.Target+" 上的搬运工还不支持失联迁移", "先更新 "+rp.Target+" 上的搬运工（valise porter deploy），再重新准备恢复")
	}
	snapshot := identity.SnapshotRef{VaultID: rp.vault.Desc.VaultID, SnapshotID: plan.SnapshotID}
	if supported {
		plan.Identity = &identity.RecoveryContext{Mode: plan.Mode, SourceSnapshot: snapshot}
	} else {
		rp.Warnings = append(rp.Warnings, "目标搬运工尚不支持世代与恢复关系，本次不建立迁移旅程关联")
	}
	if rp.SourceLost {
		// Unreachable: the source generation stays unknown (8.2.3).
		if supported {
			rp.Warnings = append(rp.Warnings, "连不上来源服务器，来源实例世代保持未知")
		}
		return nil
	}
	source, err := s.session(ctx, rp.From)
	if err != nil {
		if supported {
			rp.Warnings = append(rp.Warnings, "无法读取快照来源身份，来源实例世代保持未知")
		}
		return nil
	}
	defer source.Close()
	rp.sourceStatus = source.hello != nil && slices.Contains(source.hello.Capabilities, identity.Capability)
	if source.hello != nil && slices.Contains(source.hello.Capabilities, protection.BackupCapability) {
		var binding protection.SnapshotBinding
		err := source.Call(ctx, protection.MethodSnapshotBinding, protection.SnapshotBindingParams{SnapshotRef: snapshot}, &binding)
		if err == nil && binding.State == "bound" && binding.AgentRef != nil {
			ref := *binding.AgentRef
			if ref.ServerUID != "" && ref.InstallID != "" && ref.InstanceID != "" && ref.Generation != "" {
				rp.sourceAgent = &ref
			}
		}
	}
	if plan.Identity != nil {
		plan.Identity.SourceAgent = rp.sourceAgent
		if rp.sourceAgent == nil {
			rp.Warnings = append(rp.Warnings, "该快照没有可核实的实例世代绑定，来源实例身份未知；不会按同名补全旅程")
		}
	}
	return nil
}

func (s *Service) requireRecoveryIdentityCapability(ctx context.Context, rp *RevivePlan) error {
	if rp.Plan.Identity == nil {
		return nil
	}
	c, err := s.session(ctx, rp.Target)
	if err != nil {
		return err
	}
	defer c.Close()
	if c.hello == nil || !slices.Contains(c.hello.Capabilities, identity.Capability) {
		return valise.New("unsupported_capability", "目标不再支持计划中的恢复身份契约", "更新搬运工并重新准备恢复计划")
	}
	return nil
}
