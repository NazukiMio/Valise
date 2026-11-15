package revive

import (
	"context"
	"os"
	"path/filepath"
	"slices"

	"valise/core/environment"
	"valise/core/revive"
	"valise/core/valise"
	"valise/porter/internal/activationrecord"
	"valise/porter/internal/environments"
	"valise/porter/internal/seal"
)

func oldCleanupError(code string) error {
	reason, next := "旧环境的清理范围已变化，尚未继续删除", "重新读取原恢复记录中的清理范围，再明确确认"
	switch code {
	case "environment_cleanup_unavailable":
		reason, next = "当前尚不能清理这次恢复的旧环境", "确认新环境可用后，使用小占用的独立清理入口，或在设置中明确关闭并行后另行清理"
	case "environment_cleanup_unknown":
		reason, next = "无法核实旧目录的归属或当前环境，没有继续删除", "保留这些材料，重新检查原恢复记录和 Agent 的当前环境"
	case "environment_cleanup_contains_vault":
		reason, next = "旧环境目录内还包含备份库，没有清理", "先将备份库移出旧环境目录，再重新读取清理范围；此次操作不会删除备份"
	case "environment_cleanup_io_failed":
		reason, next = "旧环境还有材料未能清理", "当前环境的验收记录保留；重新读取固定范围后可继续清理"
	case "environment_cleanup_interrupted":
		reason, next = "已停止本次旧环境清理，部分材料可能已删除", "当前环境的验收记录保留；需要继续时重新读取并确认范围"
	}
	return valise.New(code, reason, next)
}

func oldCleanupPath(home string, rec *ActivationRecord, j *revive.Journal, item activationrecord.CleanupItem) (string, error) {
	if item.Kind == "preparation" && item.Root == "" {
		dir, err := preparationDir(home, rec.Plan.Agent, false)
		if err != nil {
			return "", err
		}
		return filepath.Join(dir, rec.Plan.OperationID), nil
	}
	if item.Kind == "program" && item.Root == "" {
		return (environments.ProgramStore{Home: home}).RevisionPath(rec.Plan.Agent, rec.Plan.Original)
	}
	if item.Kind == "data" {
		for _, root := range j.Roots {
			if root.ID == item.Root {
				return root.Prev, nil
			}
		}
	}
	return "", oldCleanupError("environment_cleanup_unknown")
}

// Claims were persisted by the trusted builder before its first copied byte.
// An original programme reused by a later operation remains owned by its
// precise former candidate revision, not by a matching display name.
func (a *ActivationRunner) oldProgramClaim(rec *ActivationRecord, r environment.Registry) (revive.ManagedRoot, error) {
	ops := []string{rec.Plan.OperationID}
	for _, previous := range r.History {
		if previous.Candidate == rec.Plan.Original || previous.Original == rec.Plan.Original {
			ops = append(ops, previous.ID)
		}
	}
	for _, op := range ops {
		p, err := loadPreparation(a.home(), rec.Plan.Agent, op)
		if err != nil {
			return revive.ManagedRoot{}, err
		}
		kind := ""
		if p.Original == rec.Plan.Original {
			kind = "program-original"
		}
		if p.Candidate == rec.Plan.Original {
			kind = "program-candidate"
		}
		for _, claim := range p.Claims {
			if kind != "" && claim.Kind == kind && claim.Root == "" {
				if claim.Removed {
					return revive.ManagedRoot{}, oldCleanupError("environment_cleanup_unknown")
				}
				fact := claim.Identity
				fact.ID = "program:"
				return fact, nil
			}
		}
	}
	return revive.ManagedRoot{}, oldCleanupError("environment_cleanup_unknown")
}

func (a *ActivationRunner) oldCleanupClaims(rec *ActivationRecord, r environment.Registry, j *revive.Journal) ([]activationrecord.CleanupItem, error) {
	p, err := loadPreparation(a.home(), rec.Plan.Agent, rec.Plan.OperationID)
	if err != nil || p.Original != rec.Plan.Original || p.Candidate != rec.Plan.Candidate || p.Plan.OpID != rec.Plan.DataOpID || p.OriginalMaterials != rec.Plan.OriginalMaterials {
		return nil, oldCleanupError("environment_cleanup_unknown")
	}
	var items []activationrecord.CleanupItem
	for i, root := range j.Roots {
		if i >= len(j.ManagedData.OriginalRoots) {
			return nil, oldCleanupError("environment_cleanup_unknown")
		}
		fact := j.ManagedData.OriginalRoots[i]
		if !slices.Contains(p.Directories, fact) {
			return nil, oldCleanupError("environment_cleanup_unknown")
		}
		fact.ID = "data:" + root.ID
		items = append(items, activationrecord.CleanupItem{Kind: "data", Root: root.ID, Identity: fact})
	}
	program, err := a.oldProgramClaim(rec, r)
	if err != nil {
		return nil, oldCleanupError("environment_cleanup_unknown")
	}
	items = append(items, activationrecord.CleanupItem{Kind: "program", Identity: program})
	for _, claim := range p.Claims {
		if claim.Kind == "workspace" && claim.Root == "" && !claim.Removed {
			fact := claim.Identity
			fact.ID = "preparation:"
			return append(items, activationrecord.CleanupItem{Kind: "preparation", Identity: fact}), nil
		}
	}
	return nil, oldCleanupError("environment_cleanup_unknown")
}

func (a *ActivationRunner) oldCleanupLeases(rec *ActivationRecord) ([]*os.File, error) {
	p, err := loadPreparation(a.home(), rec.Plan.Agent, rec.Plan.OperationID)
	if err != nil || p.Probe == nil || !p.Probe.ProcessesStopped {
		return nil, oldCleanupError("environment_cleanup_unknown")
	}
	if c := rec.Cleanup; c != nil && c.ProbeStopped {
		for _, item := range c.Items {
			if item.Kind != "preparation" || !item.Intent {
				continue
			}
			dir, err := oldCleanupPath(a.home(), rec, nil, item)
			if err != nil {
				return nil, err
			}
			// Only a persisted stopped proof plus our deletion intent can
			// explain a lease removed by this interrupted cleanup.
			lease := filepath.Join(dir, p.ProbeWorkspace, "control/lifetime.lock")
			if _, err := os.Lstat(lease); os.IsNotExist(err) {
				return nil, nil
			}
		}
	}
	leases, err := holdPreparationProbe(a.home(), p)
	if err != nil {
		return nil, oldCleanupError("environment_cleanup_unknown")
	}
	return leases, nil
}

func oldCleanupChain(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return oldCleanupError("environment_cleanup_unknown")
	}
	parent := filepath.Dir(path)
	real, err := filepath.EvalSymlinks(parent)
	if err != nil || real != parent {
		return oldCleanupError("environment_cleanup_unknown")
	}
	return nil
}

func (a *ActivationRunner) protectOldCleanup(ctx context.Context, rec *ActivationRecord, path, kind string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := oldCleanupChain(path); err != nil {
		return err
	}
	roots, err := acceptedCleanupRoots(a.home())
	if err != nil {
		return oldCleanupError("environment_cleanup_unknown")
	}
	p := seal.Paths{Home: a.home()}
	roots = append(roots, filepath.Join(a.home(), ".config", "valise"), filepath.Dir(p.Vaults()))
	if kind == "data" {
		roots = append(roots, p.State())
	}
	current, err := (environments.ProgramStore{Home: a.home()}).RevisionPath(rec.Plan.Agent, rec.Plan.Candidate)
	if err != nil {
		return err
	}
	roots = append(roots, current)
	for _, root := range roots {
		// Registered aliases protect their real data too. Missing roots still
		// protect the declared path; no directory is created during review.
		paths := []string{filepath.Clean(root)}
		if real, err := filepath.EvalSymlinks(root); err == nil {
			paths = append(paths, real)
		}
		for _, protected := range paths {
			if within(path, protected) || within(protected, path) {
				return oldCleanupError("environment_cleanup_unknown")
			}
		}
	}
	return nil
}

func inspectOldCleanupIdentity(path string, item activationrecord.CleanupItem) (bool, error) {
	fact, err := managedRootIdentity(item.Identity.ID, path)
	if os.IsNotExist(err) {
		if item.Intent || item.Removed {
			return false, nil
		}
		return false, oldCleanupError("environment_cleanup_unknown")
	}
	if err != nil || item.Removed || fact != item.Identity {
		return false, oldCleanupError("environment_cleanup_unknown")
	}
	return true, nil
}
