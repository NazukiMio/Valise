package revive

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"syscall"

	"valise/core/environment"
	"valise/core/identity"
	"valise/core/profile"
	"valise/core/revive"
	"valise/core/valise"
	porterenroll "valise/porter/internal/enroll"
	"valise/porter/internal/environments"
	"valise/porter/internal/isolated"
	"valise/porter/internal/managedruntime"
	"valise/porter/internal/seal"
)

func preparationCleanupError(code string) error {
	reason, next := "当前准备材料尚不能清理", "重新读取本次操作，核实原环境和仍在使用的材料"
	switch code {
	case "environment_prepare_already_switching":
		reason, next = "这次恢复已经开始切换，不能作为准备取消", "回到原恢复记录核实运行状态，再明确选择切回"
	case "environment_prepare_process_unknown":
		reason, next = "隔离进程可能仍在使用本次材料", "等待本次隔离进程退出后重新核实；当前 Agent 不会被停止"
	case "environment_prepare_cleanup_changed":
		reason, next = "清理范围或环境状态已变化，尚未删除材料", "重新查看本次清理范围后再确认"
	case "environment_prepare_cleanup_unknown":
		reason, next = "材料目录与本次准备记录不吻合，尚未删除", "保留该目录并核实本次操作；不会按目录名字猜测所有权"
	}
	return valise.New(code, reason, next)
}

// CancelEnvironmentPreparation never stops a service, swaps data or deletes.
// Formal activation, even an incomplete stop intent, has a separate Return.
func (r *Runner) CancelEnvironmentPreparation(ctx context.Context, agent identity.AgentRef, op string) (*EnvironmentPreparation, error) {
	return r.cancelEnvironmentPreparation(ctx, agent, op, managedruntime.Controller{Home: r.Store.Home})
}
func (r *Runner) cancelEnvironmentPreparation(ctx context.Context, agent identity.AgentRef, op string, control managedruntime.Controller) (*EnvironmentPreparation, error) {
	unlock, err := (seal.Paths{Home: r.Store.Home}).LockFor("environment_cancel_preparation")
	if err != nil {
		return nil, err
	}
	defer unlock()
	p, err := loadPreparation(r.Store.Home, agent, op)
	if err != nil {
		return nil, err
	}
	if slices.Contains([]string{"cancelled", "cleaning", "cleaned"}, p.State) {
		return p, nil
	}
	if _, _, err := r.preparationUnchanged(ctx, p, control); err != nil {
		return p, err
	}
	leases, err := holdPreparationProbe(r.Store.Home, p)
	if err != nil {
		return p, err
	}
	defer closePreparationLeases(leases)
	activation, err := loadActivation(r.Store.Home, agent, op)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return p, err
	}
	if activation != nil && (!slices.Contains([]string{"ready", "needs_review", "cancelled"}, activation.State) || len(activation.Facts) != 0 || activation.Plan.DataOpID != p.Plan.OpID || !managedPreparationParentMatches(p, activation)) {
		return p, preparationCleanupError("environment_prepare_already_switching")
	}
	if p.State != "cancelling" {
		p.CancelFrom = p.State
		p.State = "cancelling"
		if err := savePreparation(r.Store.Home, p, false); err != nil {
			return p, err
		}
	}
	if activation != nil && activation.State != "cancelled" {
		activation.State, activation.IssueCode = "cancelled", ""
		if err := saveActivation(r.Store.Home, activation); err != nil {
			return p, err
		}
	}
	store := environments.Store{Home: r.Store.Home}
	registry, err := store.Load(agent)
	if err == nil && registry.Operation != nil && registry.Operation.ID == op && registry.Operation.Phase != environment.Cancelled {
		registry, err = store.Update(agent, registry.Revision, func(v environment.Registry) (environment.Registry, error) { return v.CancelPreparation(op) })
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return p, err
	}
	if registry.Operation != nil && registry.Operation.ID == op {
		p.Original, p.Candidate = registry.Operation.Original, registry.Operation.Candidate
	}
	if err := r.cancelPreparationChild(p); err != nil {
		return p, err
	}
	p.State = "cancelled"
	if err := savePreparation(r.Store.Home, p, false); err != nil {
		return p, err
	}
	return p, nil
}

// preparationUnchanged proves first native preparation has not taken over.
// A restarted original with the same definition is observable; no service or
// programme health is inferred, and no source byte is needed to cancel it.
func (r *Runner) preparationUnchanged(ctx context.Context, p *EnvironmentPreparation, control managedruntime.Controller) (managedruntime.NativeRef, environment.Registry, error) {
	var zero managedruntime.NativeRef
	var empty environment.Registry
	if p.ManagedSource != nil {
		current, err := r.managedPreparationUnchanged(ctx, p, control, false)
		if err != nil {
			return zero, empty, err
		}
		return zero, current.Registry, nil
	}
	if err := porterenroll.ValidateIdentity(ctx, r.Store.Home, p.Params.Agent); err != nil {
		return zero, empty, err
	}
	current, err := porterenroll.ReadProfile(r.Store.Home, p.Params.Agent.InstanceID)
	if err != nil || current == nil || environments.Digest(current) != environments.Digest(p.Plan.Profile) {
		return zero, empty, preparationError("environment_prepare_changed")
	}
	if err := control.RequireAgentAbsent(ctx, p.Params.Agent); err != nil {
		return zero, empty, preparationCleanupError("environment_prepare_already_switching")
	}
	roots, err := planDirectoryFacts(p.Plan.Roots)
	if err != nil || !slices.Equal(roots, p.Directories) {
		return zero, empty, preparationError("environment_prepare_changed")
	}
	if err := r.planShared(ctx, p.Params.Agent.InstanceID, profile.ServiceUnit{Scope: "user", Unit: p.Native.Unit}, p.Plan.Roots); err != nil {
		return zero, empty, err
	}
	native, _, err := control.InspectNative(ctx, p.Plan.Profile, profile.ServiceUnit{Scope: "user", Unit: p.Native.Unit})
	if err != nil || native.Definition != p.Native.Definition || native.Executable != p.Native.Executable {
		return zero, empty, preparationError("environment_prepare_source")
	}
	registry, err := (environments.Store{Home: r.Store.Home}).Load(p.Params.Agent)
	if errors.Is(err, os.ErrNotExist) {
		return native, empty, nil
	}
	if err != nil {
		return zero, empty, err
	}
	if registry.Active != environment.A || registry.Slots[0].Ref != (environment.Ref{Slot: environment.A, Revision: 1}) || registry.Slots[0].State != environment.Active || len(registry.History) != 0 {
		return zero, empty, preparationCleanupError("environment_prepare_already_switching")
	}
	if o := registry.Operation; o != nil {
		if o.ID != p.OperationID || o.Snapshot.VaultID != p.Params.Input.VaultID || o.Snapshot.SnapshotID != p.Plan.SnapshotID || !slices.Contains([]environment.Phase{environment.Building, environment.Prepared, environment.Cancelled}, o.Phase) || o.Candidate != (environment.Ref{Slot: environment.B, Revision: 1}) {
			return zero, empty, preparationCleanupError("environment_prepare_already_switching")
		}
	} else if registry.Slots[1].Revision != 0 {
		return zero, empty, preparationCleanupError("environment_prepare_already_switching")
	}
	return native, registry, nil
}

func holdPreparationProbe(home string, p *EnvironmentPreparation) ([]*os.File, error) {
	unknown := p.State == "probing" || p.CancelFrom == "probing" || p.Probe != nil && !p.Probe.ProcessesStopped
	if p.ProbeTracking == "" {
		if unknown {
			return nil, preparationCleanupError("environment_prepare_process_unknown")
		}
		return nil, nil
	}
	if p.ProbeTracking != "bwrap-sync-fd/1" || p.ProbeWorkspace == "" {
		return nil, preparationCleanupError("environment_prepare_process_unknown")
	}
	for _, claim := range p.Claims {
		if claim.Kind == "workspace" && claim.Removed {
			path, _ := preparationResource(home, p, "workspace", "")
			if _, err := os.Lstat(path); os.IsNotExist(err) {
				return nil, nil
			}
			return nil, preparationCleanupError("environment_prepare_cleanup_unknown")
		}
	}
	dir, err := preparationWorkspace(home, p, false)
	if os.IsNotExist(err) && p.State == "cleaning" && p.CleanupReview != "" {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	workspace := filepath.Join(dir, p.ProbeWorkspace)
	for _, claim := range p.Claims {
		if claim.Kind != "workspace" && claim.Kind != "probe" {
			continue
		}
		path, err := preparationResource(home, p, claim.Kind, "")
		if err != nil {
			return nil, err
		}
		fact, err := managedRootIdentity(claim.Identity.ID, path)
		if err != nil || fact != claim.Identity {
			return nil, preparationCleanupError("environment_prepare_cleanup_unknown")
		}
	}
	if p.ProbeLease.Inode != 0 {
		st, err := os.Lstat(filepath.Join(workspace, "control/lifetime.lock"))
		if err != nil {
			return nil, preparationCleanupError("environment_prepare_process_unknown")
		}
		stat, ok := st.Sys().(*syscall.Stat_t)
		if !ok || uint64(stat.Dev) != p.ProbeLease.Device || stat.Ino != p.ProbeLease.Inode {
			return nil, preparationCleanupError("environment_prepare_process_unknown")
		}
	}
	if _, err := managedRootIdentity("probe", workspace); err != nil {
		return nil, err
	}
	lease, err := isolated.HoldProbeStopped(workspace)
	// New controller always creates its lease before starting bwrap. Missing
	// lease under a valid workspace therefore means it never launched.
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, preparationCleanupError("environment_prepare_process_unknown")
	}
	return []*os.File{lease}, nil
}
func closePreparationLeases(files []*os.File) {
	for _, f := range files {
		f.Close()
	}
}

// The child journal records unchanged directories and is archived, without
// inventing a service restart or gateway proof. Partial stages without a saved
// child remain owned by the parent's directory claims.
func (r *Runner) cancelPreparationChild(p *EnvironmentPreparation) error {
	pending := r.Store.path(p.Plan.OpID)
	done := filepath.Join(r.Store.DoneDir(), p.Plan.OpID+".json")
	if _, err := os.Lstat(pending); os.IsNotExist(err) {
		if _, err := os.Lstat(done); os.IsNotExist(err) {
			return nil
		}
	}
	j, err := r.Store.Load(p.Plan.OpID)
	if err != nil {
		return err
	}
	if environments.Digest(j.Plan) != environments.Digest(p.Plan) || j.ManagedData == nil || j.ManagedData.OperationID != p.OperationID || checkManagedData(r.Store, j, environment.Cancelled) != nil {
		return managedDataError()
	}
	for _, h := range j.History {
		if h.State != revive.StatePlanned && h.State != revive.StateRolledBack {
			return preparationCleanupError("environment_prepare_already_switching")
		}
	}
	if j.State != revive.StatePlanned && j.State != revive.StateRolledBack {
		return preparationCleanupError("environment_prepare_already_switching")
	}
	if j.State == revive.StatePlanned {
		j.Rollback = &revive.RollbackEvidence{Directories: "unchanged", Program: "not_needed", Service: "not_started", Health: "not_checked"}
		j.Error = "准备已取消，未交换当前目录"
		if err := r.Store.Set(j, revive.StateRolledBack); err != nil {
			return err
		}
	}
	if _, err := os.Lstat(pending); err == nil {
		return r.Store.Finish(j)
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}
