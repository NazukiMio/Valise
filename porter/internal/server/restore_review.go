package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"

	"valise/core/identity"
	"valise/core/protection"
	"valise/core/revive"
	"valise/core/rpc"
	"valise/core/valise"
	porterrevive "valise/porter/internal/revive"
)

func restoreReview(s *session, op string) (*revive.ReviewResult, error) {
	store := porterrevive.Store{Home: s.cfg.Home}
	j, err := store.Load(op)
	if err != nil {
		return nil, err
	}
	if j.Plan.OpID != op {
		return nil, valise.New("journal_corrupt", "操作记录与请求不一致", "停止操作并检查交换日志")
	}
	_, err = os.Stat(filepath.Join(store.DoneDir(), op+".json"))
	r := &revive.ReviewResult{Journal: *j, Complete: err == nil}
	for _, root := range j.Roots {
		for _, path := range []string{root.Prev, root.Stage, root.Failed} {
			if _, err := os.Lstat(path); err == nil {
				r.Kept = append(r.Kept, path)
			}
		}
	}
	available := porterrevive.LegacyAvailable(context.Background(), store, j)
	if !r.Complete && available {
		if !revive.Final(j.State) || j.State == revive.StateRolledBack {
			r.Actions = append(r.Actions, "back")
		}
		if porterrevive.LegacyCanContinue(context.Background(), store, j) && (!j.Plan.TestOnly && (j.Plan.Mode != revive.ModeMigrate && j.State != revive.StateRollingBack && j.State != revive.StateRolledBack && (j.State != revive.StatePlanned || j.StageVerified && j.ManifestSaved)) || !j.Plan.TestOnly && revive.Final(j.State) && j.State != revive.StateRolledBack) {
			r.Actions = append(r.Actions, "continue")
		}
	}
	r.Digest, err = protection.Digest(struct {
		Journal  revive.Journal
		Complete bool
		Actions  []string
	}{*j, r.Complete, r.Actions})
	return r, err
}

func init() {
	methods[revive.MethodReview] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := decode[revive.OpParams](raw)
		if err != nil {
			return nil, err
		}
		return restoreReview(s, p.OpID)
	}
	methods[revive.MethodResumeChecked] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := decode[revive.CheckedResumeParams](raw)
		if err != nil {
			return nil, err
		}
		review, err := restoreReview(s, p.OpID)
		if err != nil {
			return nil, err
		}
		if review.Digest != p.Digest || !slices.Contains(review.Actions, p.Action) {
			return nil, valise.New("plan_stale", "交换日志已改变或这个分支不再可用", "重新核实原操作后再确认")
		}
		if err := restoreAccess(s, p.OpID, revive.MethodResumeChecked, p.Action); err != nil {
			return nil, err
		}
		runner := s.runner(ctx)
		if p.Action == "continue" && review.Journal.State == revive.StatePlanned {
			if !review.Journal.ManifestSaved {
				return nil, valise.New("stage_unverified", "旧操作没有可重新校验的暂存清单", "先退回，再重新准备恢复")
			}
			ver, err := porterrevive.StageVerify(runner.Store, revive.StageVerifyParams{OpID: p.OpID, Manifest: review.Journal.Manifest})
			if err != nil {
				return nil, err
			}
			if len(ver.Problems) > 0 {
				return nil, valise.New("stage_verify_failed", "暂存材料已经变化", "退回本次操作并重新准备")
			}
			disposition := identity.Unresolved
			if review.Journal.Plan.Mode == revive.ModeClone {
				disposition = identity.ExclusiveOmitted
			}
			if err = runner.RecordDisposition(p.OpID, disposition); err != nil {
				return nil, err
			}
		}
		return runner.Resume(context.WithoutCancel(ctx), revive.ResumeParams{OpID: p.OpID, Action: p.Action})
	}
}

func restoreAccess(s *session, op, method, action string) error {
	j, err := (porterrevive.Store{Home: s.cfg.Home}).Load(op)
	if err != nil {
		return err
	}
	if action == "back" && (j.State == revive.StatePlanned || j.State == revive.StateStopped) && !j.NPMInstalled && !j.ServiceInstalled {
		return nil
	}
	if j.Plan.Body.Action == revive.BodyNPMInstall || j.Plan.InstallService {
		return s.require(rpc.TierAdmin, method)
	}
	return nil
}
