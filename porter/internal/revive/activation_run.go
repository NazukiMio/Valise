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

// Activate is exactly-once at the transaction boundary. A repeated request is
// a query, including after an interrupted intent. Explicit Return is separate.
func (a *ActivationRunner) Activate(ctx context.Context, agent identity.AgentRef, op string) (*ActivationReview, error) {
	if ctx == nil {
		return nil, activationError("activation_materials_unavailable")
	}
	previous, err := loadActivation(a.home(), agent, op)
	if err != nil {
		return nil, err
	}
	if previous.State != "ready" {
		return a.Query(ctx, agent, op)
	}
	if a.verify == nil {
		return nil, activationError("activation_materials_unavailable")
	}
	unlock, err := (seal.Paths{Home: a.home()}).LockFor("environment_activate")
	if err != nil {
		return nil, err
	}
	defer unlock()
	rec, err := loadActivation(a.home(), agent, op)
	if err != nil {
		return nil, err
	}
	if rec.State != "ready" {
		return a.Query(ctx, agent, op)
	}
	if a.verify == nil {
		return nil, activationError("activation_materials_unavailable")
	}
	return a.activateLocked(ctx, rec)
}

func (a *ActivationRunner) activateLocked(ctx context.Context, rec *ActivationRecord) (*ActivationReview, error) {
	agent, op := rec.Plan.Agent, rec.Plan.OperationID
	r, err := a.registry(rec)
	if err != nil || r.Operation.Phase != environment.Prepared {
		return nil, activationError("activation_stale")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Recheck both immutable programme manifests before disturbing A. The
	// adapter must be reconstructed from persisted bindings after reconnect.
	for _, p := range []struct {
		ref    environment.Ref
		digest string
	}{{rec.Plan.Original, rec.Plan.OriginalMaterials}, {rec.Plan.Candidate, rec.Plan.CandidateMaterials}} {
		if a.verify(ctx, agent, p.ref, p.digest) != nil {
			rec.FailureCode = "activation_materials_changed"
			rec.IssueCode = rec.FailureCode
			if err := a.mark(rec, "needs_review"); err != nil {
				return nil, err
			}
			return a.Query(ctx, agent, op)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	original, err := a.archive().Remembered(agent, rec.OriginalDefinition)
	if err != nil {
		return nil, err
	}
	if err := a.verifyNativeSource(ctx, rec); err != nil {
		rec.FailureCode, rec.IssueCode = "activation_native_source_changed", "activation_native_source_changed"
		if err := a.mark(rec, "needs_review"); err != nil {
			return nil, err
		}
		return a.Query(ctx, agent, op)
	}
	ob, err := a.readOriginal(ctx, rec, original)
	if err != nil || !ob.Running || ob.PendingJob || (ob.UnitFileState == "enabled") != rec.EnableOnBoot {
		rec.IssueCode = "activation_original_unknown"
		if err := a.mark(rec, "needs_review"); err != nil {
			return nil, err
		}
		return a.Query(ctx, agent, op)
	}
	j, err := a.runner.Store.Load(rec.Plan.DataOpID)
	if err != nil {
		return nil, err
	}
	if _, _, _, err := reviewManagedStage(ctx, j); err != nil {
		return nil, err // Do not stop A for already changed candidate data.
	}
	if err := a.mark(rec, "running"); err != nil {
		return nil, err
	}
	forward, cancel := context.WithTimeout(context.WithoutCancel(ctx), 4*time.Minute)
	defer cancel()
	if err := a.forward(forward, rec); err != nil {
		rec.FailureCode = activationFailureCode(err)
		if err := a.mark(rec, "reverting"); err != nil {
			return nil, err
		}
		cleanup, done := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Minute)
		defer done()
		if err := a.returnLocked(cleanup, rec); err != nil {
			rec.IssueCode = activationFailureCode(err)
			if saveErr := a.mark(rec, "needs_review"); saveErr != nil {
				return nil, saveErr
			}
			return a.queryAfter(ctx, agent, op)
		}
	}
	return a.queryAfter(ctx, agent, op)
}

func (a *ActivationRunner) forward(ctx context.Context, rec *ActivationRecord) error {
	original, err := a.archive().Remembered(rec.Plan.Agent, rec.OriginalDefinition)
	if err != nil {
		return err
	}
	candidate, err := a.archive().Remembered(rec.Plan.Agent, rec.CandidateDefinition)
	if err != nil {
		return err
	}
	if err := a.command(rec, "disable_original", func() (managedruntime.ActionResult, error) {
		if ref := rec.Plan.NativeOriginal; ref != nil {
			return a.runtime.DisableNativeStartup(ctx, *ref)
		}
		return a.runtime.DisableStartup(ctx, original)
	}); err != nil {
		return err
	}
	if err := a.transition(rec, environment.Registry.StopOriginal); err != nil {
		return err
	}
	if err := a.command(rec, "stop_original", func() (managedruntime.ActionResult, error) {
		var result managedruntime.ActionResult
		var err error
		if ref := rec.Plan.NativeOriginal; ref != nil {
			result, err = a.runtime.StopNative(ctx, *ref)
		} else {
			result, err = a.runtime.Stop(ctx, original)
		}
		if err == nil && !result.Observed.Stopped {
			err = managedruntime.ErrState
		}
		return result, err
	}); err != nil {
		return err
	}
	if err := a.transition(rec, environment.Registry.OriginalStopped); err != nil {
		return err
	}
	if err := a.nativeDown(ctx, rec); err != nil {
		return err
	}
	if err := a.fact(rec, "place_data", func(f *ActivationFact) error {
		result, err := a.runner.ExchangeManagedData(ctx, rec.Plan.DataOpID)
		if result != nil {
			f.DataState = result.State
		}
		if err == nil && (result == nil || result.State != revive.StateRestored) {
			err = managedruntime.ErrState
		}
		return err
	}); err != nil {
		return err
	}
	expected := original.Digest()
	if rec.Plan.NativeOriginal != nil {
		expected = ""
	}
	if err := a.fact(rec, "install_candidate", func(*ActivationFact) error { return a.runtime.Install(ctx, candidate, expected) }); err != nil {
		return err
	}
	if err := a.verify(ctx, rec.Plan.Agent, rec.Plan.Candidate, rec.Plan.CandidateMaterials); err != nil {
		return activationError("activation_materials_changed")
	}
	if err := a.nativeDown(ctx, rec); err != nil {
		return err
	}
	if err := a.transition(rec, environment.Registry.StartCandidate); err != nil {
		return err
	}
	if err := a.command(rec, "start_candidate", func() (managedruntime.ActionResult, error) {
		result, err := a.runtime.Start(ctx, candidate)
		if err == nil && !result.Observed.Running {
			err = managedruntime.ErrState
		}
		return result, err
	}); err != nil {
		return err
	}
	if err := a.gateway(ctx, rec, "gateway_candidate", candidate, false); err != nil {
		return err
	}
	if err := a.nativeDown(ctx, rec); err != nil {
		return err
	}
	if err := a.startup(ctx, rec, "startup_candidate", candidate, false); err != nil {
		return err
	}
	if err := a.transition(rec, environment.Registry.GatewayPassed); err != nil {
		return err
	}
	return a.mark(rec, "awaiting_human")
}

func (a *ActivationRunner) nativeDown(ctx context.Context, rec *ActivationRecord) error {
	if ref := rec.Plan.NativeOriginal; ref != nil {
		ob, err := a.runtime.ReadNative(ctx, *ref)
		if err != nil || !ob.Stopped || ob.UnitFileState != "disabled" {
			return activationError("activation_native_not_stopped")
		}
	}
	return nil
}

func (a *ActivationRunner) gateway(ctx context.Context, rec *ActivationRecord, name string, d managedruntime.Definition, original bool) error {
	p := rec.Plan.CandidateProfile
	if original {
		p = rec.Plan.OriginalProfile
	}
	return a.fact(rec, name, func(f *ActivationFact) error {
		// Readiness can take time after systemd reports running. Poll only
		// this declared, owned endpoint; no arbitrary URL or response body.
		deadline, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		for {
			result, err := a.runtime.CheckHTTP(deadline, d, p)
			f.Gateway = &result
			if err == nil && result.GatewayPassed {
				return nil
			}
			if result.HTTPResponded {
				return managedruntime.ErrState
			}
			if !result.UnitRunning {
				return managedruntime.ErrState
			}
			select {
			case <-deadline.Done():
				return deadline.Err()
			case <-time.After(100 * time.Millisecond):
			}
		}
	})
}

func (a *ActivationRunner) startup(ctx context.Context, rec *ActivationRecord, name string, d managedruntime.Definition, original bool) error {
	p := rec.Plan.CandidateProfile
	if original {
		p = rec.Plan.OriginalProfile
	}
	return a.command(rec, name, func() (managedruntime.ActionResult, error) {
		if rec.EnableOnBoot {
			return a.runtime.EnableStartup(ctx, d, p)
		}
		return a.runtime.DisableStartup(ctx, d)
	})
}
