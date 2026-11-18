package service

import (
	"context"
	"errors"

	"valise/core/protection"
	"valise/core/valise"
	"valise/core/vault"
	"valise/core/vaultrpc"
)

// PromoteSnapshot publishes an independent savepoint on its source server
// and follows the instance's existing copy settings. Copy errors explicitly
// preserve the fact that the savepoint was published.
func (s *Service) PromoteSnapshot(ctx context.Context, host, instance, snapshotID, note string) error {
	stopOp := s.BeginOperation()
	defer stopOp()
	note, err := vault.NormalizeNote(note)
	if err != nil {
		return err
	}
	var result vaultrpc.SnapshotPromoteResult
	p := vaultrpc.SnapshotPromoteParams{InstanceID: instance, SnapshotID: snapshotID, Note: note}
	if err := s.call(ctx, host, vaultrpc.MethodSnapshotPromote, p, &result); err != nil {
		return err
	}
	if result.SnapshotID != snapshotID || result.Leaf != instance+"/"+vault.SavepointsPeriod {
		return valise.New("result_unknown", "服务器未返回这个存档点的保存位置", "刷新旅程后核对")
	}
	r := s.timelineReader()
	defer r.Invalidate()
	r.Invalidate()

	err = s.copyPromotedOnServer(ctx, host, instance)
	local, copyErr := s.AutoCopy(ctx, host, TriggerBackup, AutoCopyOptions{Instance: instance})
	err = errors.Join(err, copyErr)
	incomplete := err != nil
	if local != nil {
		for _, loc := range local.Locations {
			switch loc.State {
			case CopyFailed, CopyUnmounted, CopyBusy:
				incomplete = true
			}
		}
	}
	if incomplete {
		return valise.New("snapshot_promoted_copy_incomplete", "手动存档点已保存，备份位置未全部同步", "打开备份位置查看状态并重试同步")
	}
	return nil
}

func (s *Service) copyPromotedOnServer(ctx context.Context, host, instance string) error {
	refs, err := s.agentRefs(ctx, host, []string{instance})
	if err != nil {
		return err
	}
	var targets []string
	if ref, ok := refs[instance]; ok {
		view, err := s.GetProtection(ctx, host, protection.GetParams{AgentRef: ref})
		if err != nil {
			return err
		}
		if plan := view.ServerPlan; plan != nil {
			for _, dest := range plan.ServerCopies {
				if dest.Enabled != nil && *dest.Enabled && dest.TargetRef.AuthorityID == ref.ServerUID {
					targets = append(targets, dest.TargetRef.ID)
				}
			}
			return s.syncPromotedTargets(ctx, host, targets)
		}
	}
	status, err := s.VaultStatus(ctx, host)
	if err != nil {
		return err
	}
	for _, dest := range status.Targets {
		if dest.Auto != nil {
			targets = append(targets, dest.Name)
		}
	}
	return s.syncPromotedTargets(ctx, host, targets)
}

func (s *Service) syncPromotedTargets(ctx context.Context, host string, targets []string) error {
	var errs error
	for _, target := range targets {
		_, err := s.Sync(ctx, host, target)
		errs = errors.Join(errs, err)
	}
	return errs
}
