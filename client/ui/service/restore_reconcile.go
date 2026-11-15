package service

import (
	"context"
	"slices"
	"valise/core/identity"
	"valise/core/revive"
)

// InspectRestore never replays an operation. The porter takes its mutation
// lock while reading, so an active swap is reported busy rather than resumable.
func (s *Service) InspectRestore(ctx context.Context, t RestoreTask) (RestoreTask, *revive.ReviewResult, error) {
	if t.Kind != "" {
		return t, nil, restoreError("environment_operation_required", "这项恢复使用并行准备记录，请从原操作核实，不能走旧目录交换")
	}
	if t.TrustDigest != "" {
		trust, err := s.restoreTrust(t.Target)
		if err != nil {
			return t, nil, err
		}
		if trust != t.TrustDigest {
			return t, nil, restoreError("plan_stale", "目标连接或信任身份已变化，请先检查服务器设置")
		}
	}
	o, err := s.ownerPorter(ctx, t.Target)
	if err != nil {
		return t, nil, err
	}
	var r revive.ReviewResult
	if err = s.callOwner(ctx, o, nil, revive.MethodReview, revive.OpParams{OpID: t.OpID}, &r); err != nil {
		return t, nil, err
	}
	j := r.Journal
	if j.Plan.OpID != t.OpID || j.Plan.InstanceID != t.Instance || j.Plan.SnapshotID != t.Snapshot.SnapshotID || j.Plan.Mode != t.Mode {
		return t, nil, restoreError("source_conflict", "服务器操作与本机任务引用不一致")
	}
	if j.Plan.Identity != nil && j.Plan.Identity.SourceSnapshot.VaultID != t.Snapshot.VaultID {
		return t, nil, restoreError("source_conflict", "操作引用的备份库身份不一致")
	}
	t.Rollback = j.Rollback
	if j.RecoveryTarget != nil {
		ref := j.RecoveryTarget.Ref
		t.TargetAgent = &ref
	}
	t.Verified = j.StageVerified
	t.SwapState = j.State
	t.State = "needs_recovery"
	if r.Complete {
		switch j.State {
		case revive.StateHealthy, revive.StateRestored:
			t.State = "succeeded"
		case revive.StateRolledBack:
			t.State = "failed"
			if t.CancelRequested {
				untouched := true
				for _, h := range j.History {
					untouched = untouched && (h.State == revive.StatePlanned || h.State == revive.StateRolledBack)
				}
				if untouched {
					t.State = "cancelled"
				}
			}
		}
	}
	t.Prev = r.Kept
	if j.RecoveryTarget != nil {
		var binding identity.Binding
		if s.call(ctx, t.Target, identity.MethodGet, identity.GetParams{InstanceID: t.Instance}, &binding) == nil {
			for _, rel := range binding.Relations {
				if rel.OperationRef == t.OpID {
					t.Relation = true
				}
			}
		}
	}
	return t, &r, s.saveRestore(t)
}

// ResumeRestore accepts only the fresh reviewed branch from a human adapter.
// Migration interruption offers back only until the target is already final:
// continuing earlier would require renewed cross-host isolation evidence.
func (s *Service) ResumeRestore(ctx context.Context, t RestoreTask, r *revive.ReviewResult, action string) (RestoreTask, error) {
	if t.Kind != "" {
		return t, restoreError("environment_operation_required", "这项操作不能使用旧恢复的继续或退回入口")
	}
	if r == nil || !slices.Contains(r.Actions, action) {
		return t, restoreError("plan_stale", "没有现场核实允许的恢复分支")
	}
	if t.TrustDigest != "" {
		trust, err := s.restoreTrust(t.Target)
		if err != nil {
			return t, err
		}
		if trust != t.TrustDigest {
			return t, restoreError("plan_stale", "目标连接或信任身份已变化")
		}
	}
	var err error
	var result revive.SwapResult
	accessPlan := r.Journal.Plan
	if action == "back" && (r.Journal.State == revive.StatePlanned || r.Journal.State == revive.StateStopped) && !r.Journal.NPMInstalled && !r.Journal.ServiceInstalled {
		accessPlan.Body.Action = revive.BodyKeep
		accessPlan.InstallService = false
	}
	err = s.callRestoreOperation(context.WithoutCancel(ctx), t.Target, accessPlan, nil, revive.MethodResumeChecked, revive.CheckedResumeParams{OpID: t.OpID, Action: action, Digest: r.Digest}, &result)
	if err != nil {
		return t, err
	}
	t, _, err = s.InspectRestore(context.WithoutCancel(ctx), t)
	return t, err
}
