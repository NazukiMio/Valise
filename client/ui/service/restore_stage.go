package service

import (
	"context"
	"time"
	"valise/core/revive"
)

func restoreManifest(rp *RevivePlan) []revive.ManifestEntry {
	return stageManifest(rp)
}

// RecheckRestoreStage renews only a still-matching staged plan. It is not a
// confirmation and never exchanges directories. The final answer is checked
// again by RunRestore and by the stage verifier before swap.
func (s *Service) RecheckRestoreStage(ctx context.Context, p *PreparedRestore) error {
	stopOp := s.BeginOperation()
	defer stopOp()
	if err := s.validateRestore(ctx, p, p.Revive.Plan.OpID); err != nil {
		return err
	}
	var ver revive.StageVerifyResult
	if err := s.callOwner(ctx, p.Revive.target, nil, revive.MethodStageVerify, revive.StageVerifyParams{OpID: p.Revive.Plan.OpID, Manifest: restoreManifest(p.Revive)}, &ver); err != nil {
		return err
	}
	if len(ver.Problems) > 0 {
		return restoreError("stage_verify_failed", "暂存材料已变化，不能复用旧确认")
	}
	p.mu.Lock()
	p.stageExpires = time.Now().Add(10 * time.Minute)
	p.mu.Unlock()
	return nil
}
