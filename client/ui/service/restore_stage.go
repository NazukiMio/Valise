package service

import (
	"context"
	"time"
	"valise/core/revive"
)

func restoreManifest(rp *RevivePlan) []revive.ManifestEntry {
	var man []revive.ManifestEntry
	for _, f := range rp.files {
		man = append(man, revive.ManifestEntry{Path: tarName(rp.Plan, f.Root, f.Path), Size: f.Size, SHA256: f.SHA256, Category: f.Category, SQLite: rp.sqlite[f.Root+":"+f.Path]})
	}
	return man
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
