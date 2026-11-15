package revive

import (
	"context"
	"os"
	"path/filepath"

	"valise/core/environment"
	"valise/core/protection"
	"valise/core/revive"
	"valise/core/valise"
	porterenroll "valise/porter/internal/enroll"
)

func managedDataError() error {
	return valise.New("managed_environment_required", "这些目录属于原来的并行恢复操作，不能单独切换或清理", "回到原恢复记录核实环境状态，再处理切回或清理")
}

// BeginManagedData is an internal entry, never a request handler. The parent
// executor holds the recovery lock and supplies its already authenticated
// identity, operation, and complete candidate manifest. No service is touched.
func BeginManagedData(ctx context.Context, e *porterenroll.Env, s Store, p revive.Plan, scope revive.ManagedDataRef, manifest []revive.ManifestEntry, claim ...func(string, revive.ManagedRoot) error) (*revive.Journal, error) {
	if e == nil || e.Home != s.Home {
		return nil, managedDataError()
	}
	physical, err := managedRootLayout(p)
	if err != nil {
		return nil, err
	}
	scope.OriginalRoots, scope.CandidateRoots, scope.LogicalRoots = nil, nil, nil
	bases := map[string]string{}
	for _, root := range physical {
		bases[root.ID] = root.Path
	}
	for _, root := range p.Roots {
		base, rel := root.Path, ""
		if root.SwapRoot != "" {
			base, rel = bases[root.SwapRoot], root.Rel
		}
		identity, err := managedLogicalIdentity(root.ID, base, rel)
		if err != nil {
			return nil, err
		}
		if root.SwapRoot == "" {
			scope.OriginalRoots = append(scope.OriginalRoots, identity)
		}
		if len(physical) != len(p.Roots) {
			scope.LogicalRoots = append(scope.LogicalRoots, identity)
		}
	}
	scope.PlanDigest, err = protection.Digest(p)
	if err != nil {
		return nil, err
	}
	scope.ManifestDigest, err = protection.Digest(manifest)
	if err != nil {
		return nil, err
	}
	j := &revive.Journal{Plan: p, ManagedData: &scope}
	if err := checkManagedData(s, j, environment.Building, environment.Prepared); err != nil {
		return nil, err
	}
	return stageBegin(ctx, e, s, p, &scope, claim...)
}

func managedManifestMatches(j *revive.Journal, manifest []revive.ManifestEntry) bool {
	digest, err := protection.Digest(manifest)
	return err == nil && j.ManagedData != nil && digest == j.ManagedData.ManifestDigest
}

// ExchangeManagedData only places files after the parent has verified the
// original stopped. A repeat of a completed exchange returns facts, not a replay.
// Interrupted exchanges require the parent to choose a recovery branch.
func (r *Runner) ExchangeManagedData(ctx context.Context, op string) (*revive.SwapResult, error) {
	j, err := r.Store.Load(op)
	if err != nil {
		return nil, err
	}
	if err := checkManagedData(r.Store, j, environment.OriginalDown); err != nil {
		return nil, err
	}
	if j.State == revive.StateRestored {
		return managedDataResult(j), nil
	}
	if j.State != revive.StatePlanned || !j.StageVerified || !j.ManifestSaved || !managedManifestMatches(j, j.Manifest) {
		return nil, managedDataError()
	}
	if err := checkManagedLocations(j); err != nil {
		return nil, err
	}
	if err := checkManagedStage(j); err != nil {
		return nil, err
	}
	verified, err := StageVerify(r.Store, revive.StageVerifyParams{OpID: op, Manifest: j.Manifest})
	if err != nil {
		return nil, err
	}
	if len(verified.Problems) != 0 {
		return nil, valise.New("environment_stage_changed", "候选数据在准备后发生了变化，尚未切换原目录", "核实原恢复操作并重新准备，不要启动候选环境")
	}
	return r.advance(ctx, j)
}

// RevertManagedData never stops or starts a service. The parent must first
// persist verified candidate-down evidence. Reopening done -> pending precedes
// the first filesystem change so a crash is visible as an unfinished rollback.
func (r *Runner) RevertManagedData(ctx context.Context, op string) (*revive.SwapResult, error) {
	j, err := r.Store.Load(op)
	if err != nil {
		return nil, err
	}
	if err := checkManagedData(r.Store, j, environment.CandidateDown, environment.Resuming); err != nil {
		return nil, err
	}
	if err := checkManagedLocations(j); err != nil {
		return nil, err
	}
	if j.State == revive.StateRolledBack {
		if _, err := os.Lstat(r.Store.path(op)); err == nil {
			if err := r.Store.Finish(j); err != nil {
				return nil, err
			}
		}
		return &revive.SwapResult{OpID: op, State: j.State, Rollback: j.Rollback}, nil
	}
	if err := r.Store.reopenManagedData(j); err != nil {
		return nil, err
	}
	return r.back(ctx, j, "候选环境未通过，原数据目录已退回；运行状态由原操作另行核实")
}

func managedDataResult(j *revive.Journal) *revive.SwapResult {
	r := &revive.SwapResult{OpID: j.Plan.OpID, State: j.State, Message: "候选数据已放置，尚未启动环境"}
	for _, root := range j.Roots {
		if exists(root.Prev) {
			r.Prev = append(r.Prev, root.Prev)
		}
	}
	return r
}

func (s Store) reopenManagedData(j *revive.Journal) error {
	pending, done := s.path(j.Plan.OpID), filepath.Join(s.DoneDir(), j.Plan.OpID+".json")
	_, pendingErr := os.Lstat(pending)
	_, doneErr := os.Lstat(done)
	if pendingErr == nil && os.IsNotExist(doneErr) {
		return nil
	}
	if !os.IsNotExist(pendingErr) || doneErr != nil || j.State != revive.StateRestored {
		return managedDataError()
	}
	if err := os.Rename(done, pending); err != nil {
		return err
	}
	for _, path := range []string{s.Dir(), s.DoneDir()} {
		d, err := os.Open(path)
		if err != nil {
			return err
		}
		err = d.Sync()
		d.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
