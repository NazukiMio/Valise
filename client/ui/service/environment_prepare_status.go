package service

import (
	"context"

	"valise/core/environment"
	"valise/core/identity"
	"valise/core/protection"
	"valise/core/revive"
)

func parallelSnapshotMatches(t RestoreTask, ref identity.SnapshotRef) bool {
	return ref.VaultID == t.Snapshot.VaultID && ref.SnapshotID == t.Snapshot.SnapshotID
}

func parallelTaskParams(t RestoreTask) environment.PreparationParams {
	p := environment.PreparationParams{OperationID: t.OpID}
	if t.TargetAgent != nil {
		p.Agent = *t.TargetAgent
	}
	return p
}

func (s *Service) checkParallelTask(t RestoreTask) error {
	p := parallelTaskParams(t)
	if t.Kind != RestoreKindEnvironment || !protection.ValidID(t.OpID) || protection.ValidateAgent(p.Agent) != nil || t.Instance != p.Agent.InstanceID ||
		t.Mode != revive.ModeRollback || t.Target == "" || t.Target != t.From || t.TrustDigest == "" || t.EnvironmentMode != environment.Parallel && t.EnvironmentMode != environment.Compact {
		return restoreError("record_corrupt", "并行准备记录缺少完整的 Agent 或操作身份")
	}
	trust, err := s.restoreTrust(t.Target)
	if err != nil || trust != t.TrustDigest {
		return restoreError("plan_stale", "目标连接或信任身份已变化，请先检查服务器设置")
	}
	return nil
}

// InspectParallelPreparation only queries the original operation. It never
// retries capture/upload/build, including after an uncertain first response.
func (s *Service) InspectParallelPreparation(ctx context.Context, t RestoreTask) (RestoreTask, *environment.PreparationStatus, error) {
	if err := s.checkParallelTask(t); err != nil {
		return t, nil, err
	}
	var status environment.PreparationStatus
	if err := s.call(ctx, t.Target, environment.MethodPreparationStatus, parallelTaskParams(t), &status); err != nil {
		return t, nil, err
	}
	if status.Agent != *t.TargetAgent || status.OperationID != t.OpID || !parallelSnapshotMatches(t, status.Snapshot) || status.Mode != t.EnvironmentMode {
		return t, nil, restoreError("source_conflict", "服务器的准备记录与原任务或时间点不一致")
	}
	t.State, t.Stage, t.Verified = "needs_recovery", "", false
	switch status.State {
	case "historical":
		if status.Retired && !status.CanCancel && !status.CanReviewCleanup {
			switch status.Phase {
			case environment.Accepted:
				t.State = "succeeded"
			case environment.Cancelled:
				t.State = "cancelled"
			case environment.RolledBack:
				t.State = "failed"
			}
		}
	case "prepared":
		if status.IssueCode == "" && status.Phase == environment.Prepared && status.Probe == "stopped" && status.Check != nil && status.Check.Verified() {
			t.State, t.Verified = "prepared", true
		}
	case "cancelled":
		t.State, t.CancelRequested = "cancelled", true
	case "cleaned":
		t.State, t.CancelRequested = "cleaned", true
	}
	return t, &status, s.saveRestore(t)
}

func (s *Service) CancelParallelPreparation(ctx context.Context, t RestoreTask) (RestoreTask, *environment.PreparationStatus, error) {
	if err := s.checkParallelTask(t); err != nil {
		return t, nil, err
	}
	err := s.withPreparationManagement(ctx, t.Target, func(o *ownerPorter) error {
		return s.callPreparation(ctx, o, environment.MethodPreparationCancel, parallelTaskParams(t), new(environment.PreparationStatus))
	})
	if err != nil {
		return t, nil, err
	}
	t.RuntimeRequested = false // The server verified no formal takeover occurred.
	return s.InspectParallelPreparation(ctx, t)
}

func (s *Service) ReviewParallelPreparationCleanup(ctx context.Context, t RestoreTask) (*environment.PreparationCleanupReview, error) {
	if err := s.checkParallelTask(t); err != nil {
		return nil, err
	}
	var view environment.PreparationCleanupReview
	if err := s.call(ctx, t.Target, environment.MethodPreparationCleanupReview, parallelTaskParams(t), &view); err != nil {
		return nil, err
	}
	if view.PreparationParams != parallelTaskParams(t) || !parallelSnapshotMatches(t, view.Snapshot) || len(view.Digest) != 64 ||
		view.State != "cancelled" && view.State != "cleaning" && view.State != "cleaned" {
		return nil, restoreError("source_conflict", "清理范围与原准备操作不一致")
	}
	return &view, nil
}

func (s *Service) CleanupParallelPreparation(ctx context.Context, t RestoreTask, review environment.PreparationCleanupReview) (RestoreTask, *environment.PreparationStatus, error) {
	if err := s.checkParallelTask(t); err != nil {
		return t, nil, err
	}
	if review.PreparationParams != parallelTaskParams(t) || !parallelSnapshotMatches(t, review.Snapshot) || len(review.Digest) != 64 {
		return t, nil, restoreError("plan_stale", "清理审阅已变化，请重新查看原操作的材料")
	}
	err := s.withPreparationManagement(ctx, t.Target, func(o *ownerPorter) error {
		return s.callPreparation(ctx, o, environment.MethodPreparationCleanup, environment.PreparationCleanupParams{PreparationParams: review.PreparationParams, Digest: review.Digest}, new(environment.PreparationStatus))
	})
	if err != nil {
		return t, nil, err
	}
	t.RuntimeRequested = false
	return s.InspectParallelPreparation(ctx, t)
}
