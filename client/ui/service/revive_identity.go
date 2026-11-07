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
func (s *Service) prepareRecoveryIdentity(ctx context.Context, rp *RevivePlan, plan *revive.Plan) error {
	target, err := s.session(ctx, rp.Target)
	if err != nil {
		return err
	}
	supported := target.hello != nil && slices.Contains(target.hello.Capabilities, identity.Capability)
	target.Close()
	if !supported {
		rp.Warnings = append(rp.Warnings, "目标搬运工尚不支持世代与恢复关系，本次不建立迁移旅程关联")
		return nil
	}
	plan.Identity = &identity.RecoveryContext{Mode: plan.Mode, SourceSnapshot: identity.SnapshotRef{VaultID: rp.vault.Desc.VaultID, SnapshotID: plan.SnapshotID}}
	source, err := s.session(ctx, rp.From)
	if err != nil {
		rp.Warnings = append(rp.Warnings, "无法读取快照来源身份，来源实例世代保持未知")
		return nil
	}
	defer source.Close()
	rp.sourceStatus = source.hello != nil && slices.Contains(source.hello.Capabilities, identity.Capability)
	if source.hello != nil && slices.Contains(source.hello.Capabilities, protection.BackupCapability) {
		var binding protection.SnapshotBinding
		err := source.Call(ctx, protection.MethodSnapshotBinding, protection.SnapshotBindingParams{SnapshotRef: plan.Identity.SourceSnapshot}, &binding)
		if err == nil && binding.State == "bound" && binding.AgentRef != nil {
			ref := *binding.AgentRef
			if ref.ServerUID != "" && ref.InstallID != "" && ref.InstanceID != "" && ref.Generation != "" {
				plan.Identity.SourceAgent = &ref
			}
		}
	}
	if plan.Identity.SourceAgent == nil {
		rp.Warnings = append(rp.Warnings, "该快照没有可核实的实例世代绑定，来源实例身份未知；不会按同名补全旅程")
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
