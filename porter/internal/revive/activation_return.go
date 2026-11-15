package revive

import (
	"context"
	"time"

	"valise/core/environment"
	"valise/core/identity"
	"valise/core/revive"
	"valise/porter/internal/managedruntime"
	"valise/porter/internal/seal"
)

// Return is an explicitly selected recovery branch. Merely opening a record
// uses Query and cannot call this method. The adapter is management-only.
func (a *ActivationRunner) Return(ctx context.Context, agent identity.AgentRef, op string) (*ActivationReview, error) {
	if ctx == nil {
		return nil, activationError("activation_materials_unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	unlock, err := (seal.Paths{Home: a.home()}).LockFor("environment_return")
	if err != nil {
		return nil, err
	}
	defer unlock()
	return a.returnReviewedLocked(ctx, agent, op)
}

func (a *ActivationRunner) returnReviewedLocked(ctx context.Context, agent identity.AgentRef, op string) (*ActivationReview, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rec, err := loadActivation(a.home(), agent, op)
	if err != nil {
		return nil, err
	}
	r, err := a.registry(rec)
	if err != nil {
		return nil, err
	}
	if r.Operation.Phase == environment.RolledBack {
		return a.Query(ctx, agent, op)
	}
	if a.verify == nil {
		return nil, activationError("activation_materials_unavailable")
	}
	if r.Operation.Phase == environment.Cancelled || r.Operation.Phase == environment.Cleaning || r.Operation.OriginalCleaned {
		return nil, activationError("activation_stale")
	}
	if rec.FailureCode == "" || r.Operation.Phase == environment.Accepted {
		rec.FailureCode = "user_revert"
	}
	if err := a.mark(rec, "reverting"); err != nil {
		return nil, err
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Minute)
	defer cancel()
	if err := a.returnLocked(cleanup, rec); err != nil {
		rec.IssueCode = activationFailureCode(err)
		if err := a.mark(rec, "needs_review"); err != nil {
			return nil, err
		}
	}
	return a.queryAfter(ctx, agent, op)
}

func (a *ActivationRunner) returnLocked(ctx context.Context, rec *ActivationRecord) error {
	r, err := a.registry(rec)
	if err != nil {
		return err
	}
	switch r.Operation.Phase {
	case environment.Cancelled, environment.Cleaning:
		return activationError("activation_stale")
	case environment.RolledBack:
		return a.mark(rec, "rolled_back")
	case environment.RollingBack, environment.CandidateDown, environment.Resuming:
	default:
		if err := a.transition(rec, func(r environment.Registry, id string) (environment.Registry, error) {
			return r.BeginRollback(id, rec.FailureCode)
		}); err != nil {
			return err
		}
	}
	original, err := a.archive().Remembered(rec.Plan.Agent, rec.OriginalDefinition)
	if err != nil {
		return err
	}
	installed, ob, err := a.installed(ctx, rec)
	nativeRunning := false
	if ref := rec.Plan.NativeOriginal; ref != nil {
		native, e := a.runtime.ReadNative(ctx, *ref)
		if e != nil {
			return activationError("activation_native_unknown")
		}
		if err != nil && a.runtime.RequireAbsent(ctx, original) == nil {
			if nativeMatches(rec, native) {
				nativeRunning = true
				ob = native
				err = nil
			} else if native.Stopped && native.UnitFileState == "disabled" {
				installed, ob, err = managedruntime.Definition{}, managedruntime.Observation{Stopped: true}, nil
			}
		} else if err == nil && (!native.Stopped || native.UnitFileState != "disabled") {
			return activationError("activation_native_not_stopped")
		}
	}
	if err != nil {
		return err
	}
	j, err := a.runner.Store.Load(rec.Plan.DataOpID)
	if err != nil {
		return err
	}
	if err := checkManagedLocations(j); err != nil {
		return err
	}
	// A preparation-only cancellation, or a retry after A was already
	// restored and started, must not interrupt the still-running original.
	originalRunning := (nativeRunning || installed.Digest() == original.Digest() && ob.Running) && (j.State == revive.StatePlanned || j.State == revive.StateRolledBack)
	if nativeRunning && !originalRunning {
		return activationError("activation_native_not_stopped")
	}
	if originalRunning {
		for i, root := range j.Roots {
			identity, err := managedRootIdentity(root.ID, root.Live)
			if err != nil || identity != j.ManagedData.OriginalRoots[i] {
				return managedruntime.ErrState
			}
		}
	} else if installed.Digest() != "" {
		if err := a.command(rec, "disable_return", func() (managedruntime.ActionResult, error) { return a.runtime.DisableStartup(ctx, installed) }); err != nil {
			return err
		}
		if err := a.command(rec, "stop_candidate", func() (managedruntime.ActionResult, error) {
			result, err := a.runtime.Stop(ctx, installed)
			if err == nil && !result.Observed.Stopped {
				err = managedruntime.ErrState
			}
			return result, err
		}); err != nil {
			return err
		}
	}
	r, err = a.registry(rec)
	if err != nil {
		return err
	}
	if r.Operation.Phase == environment.RollingBack {
		if err := a.transition(rec, environment.Registry.CandidateStopped); err != nil {
			return err
		}
	}
	if err := a.fact(rec, "revert_data", func(f *ActivationFact) error {
		result, err := a.runner.RevertManagedData(ctx, rec.Plan.DataOpID)
		if result != nil {
			f.DataState = result.State
		}
		if err == nil && (result == nil || result.State != revive.StateRolledBack) {
			err = managedruntime.ErrState
		}
		return err
	}); err != nil {
		return err
	}
	if err := a.verify(ctx, rec.Plan.Agent, rec.Plan.Original, rec.Plan.OriginalMaterials); err != nil {
		return activationError("activation_original_materials_changed")
	}
	if !originalRunning {
		if err := a.fact(rec, "install_original", func(*ActivationFact) error { return a.runtime.Install(ctx, original, installed.Digest()) }); err != nil {
			return err
		}
	}
	r, err = a.registry(rec)
	if err != nil {
		return err
	}
	if r.Operation.Phase == environment.CandidateDown {
		if err := a.transition(rec, environment.Registry.ResumeOriginal); err != nil {
			return err
		}
	}
	if !originalRunning {
		if err := a.nativeDown(ctx, rec); err != nil {
			return err
		}
		if err := a.command(rec, "start_original", func() (managedruntime.ActionResult, error) {
			result, err := a.runtime.Start(ctx, original)
			if err == nil && !result.Observed.Running {
				err = managedruntime.ErrState
			}
			return result, err
		}); err != nil {
			return err
		}
	}
	if nativeRunning {
		if err := a.fact(rec, "gateway_original", func(f *ActivationFact) error {
			proof, err := a.runtime.CheckNativeHTTP(ctx, *rec.Plan.NativeOriginal, rec.Plan.OriginalProfile)
			f.Gateway = &proof
			return err
		}); err != nil {
			return err
		}
		if err := a.command(rec, "startup_original", func() (managedruntime.ActionResult, error) {
			return a.runtime.RestoreNativeStartup(ctx, *rec.Plan.NativeOriginal, rec.Plan.OriginalProfile)
		}); err != nil {
			return err
		}
	} else {
		if err := a.gateway(ctx, rec, "gateway_original", original, true); err != nil {
			return err
		}
		if err := a.startup(ctx, rec, "startup_original", original, true); err != nil {
			return err
		}
	}
	if err := a.transition(rec, environment.Registry.OriginalHealthy); err != nil {
		return err
	}
	return a.mark(rec, "rolled_back")
}

func (a *ActivationRunner) queryAfter(ctx context.Context, agent identity.AgentRef, op string) (*ActivationReview, error) {
	query, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	return a.Query(query, agent, op)
}
