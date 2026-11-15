package revive

import (
	"context"
	"slices"

	"valise/core/environment"
	"valise/core/identity"
	"valise/core/revive"
	"valise/porter/internal/environments"
	"valise/porter/internal/managedruntime"
	"valise/porter/internal/seal"
)

// ReviewSwitch checks the actual frozen materials; Query alone is deliberately
// insufficient to authorize another service/data operation.
func (a *ActivationRunner) ReviewSwitch(ctx context.Context, agent identity.AgentRef, op string) (*environment.SwitchReview, error) {
	if ctx == nil {
		return nil, activationError("activation_materials_unavailable")
	}
	if _, err := loadActivation(a.home(), agent, op); err != nil {
		return nil, err // A missing query does not create even the lock directory.
	}
	unlock, err := (seal.Paths{Home: a.home()}).ObservationLock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	return a.reviewSwitchLocked(ctx, agent, op)
}

func (a *ActivationRunner) reviewSwitchLocked(ctx context.Context, agent identity.AgentRef, op string) (*environment.SwitchReview, error) {
	record, err := loadActivation(a.home(), agent, op)
	if err != nil {
		return nil, err
	}
	if historical, err := a.historicalSwitchStatus(ctx, record); err != nil {
		return nil, err
	} else if historical != nil {
		return &environment.SwitchReview{Status: *historical}, nil
	}
	q, err := a.Query(ctx, agent, op)
	if err != nil {
		return nil, err
	}
	rec := q.Record
	r, err := a.registry(rec)
	if err != nil {
		return nil, err
	}
	j, err := a.runner.Store.Load(rec.Plan.DataOpID)
	if err != nil {
		return nil, err
	}
	status, current := a.switchStatus(ctx, q, r)
	view := &environment.SwitchReview{Status: *status, EnableOnBoot: rec.EnableOnBoot}
	if r.Operation.OriginalCleaned || r.Operation.Phase == environment.Cleaning {
		view.CandidateProgramChecked = current != nil && current.Ref == rec.Plan.Candidate
		return view, nil // Missing old materials are expected here, not damage.
	}
	if a.verify == nil {
		view.Status.IssueCode = "activation_materials_unavailable"
		return view, nil
	}
	// Failed candidate bytes must not prevent a requested return, but the
	// independent original must be intact before disturbing a working B.
	view.OriginalProgramChecked = a.verify(ctx, agent, rec.Plan.Original, rec.Plan.OriginalMaterials) == nil
	view.CandidateProgramChecked = a.verify(ctx, agent, rec.Plan.Candidate, rec.Plan.CandidateMaterials) == nil
	view.ProgramsChecked = view.OriginalProgramChecked && view.CandidateProgramChecked
	view.CanConfirm = r.Operation.Phase == environment.AwaitingHuman && view.CandidateProgramChecked && current != nil &&
		current.Ref == rec.Plan.Candidate && current.Current.Running && !current.Current.PendingJob
	if !view.OriginalProgramChecked && !view.CanConfirm {
		view.Status.IssueCode = "activation_original_materials_changed"
		return view, nil
	}
	var stage string
	if rec.State == "ready" && r.Operation.Phase == environment.Prepared {
		if !view.ProgramsChecked {
			view.Status.IssueCode = "activation_materials_changed"
			return view, nil
		}
		if err := a.verifyNativeSource(ctx, rec); err != nil {
			view.Status.IssueCode = "activation_native_source_changed"
			return view, nil
		}
		view.SourceChecked = true
		stage, view.Files, view.Bytes, err = reviewManagedStage(ctx, j)
		if err != nil {
			view.Status.IssueCode = "activation_stage_changed"
			return view, nil
		}
		view.StageChecked = true
		original, err := a.archive().Remembered(agent, rec.OriginalDefinition)
		if err != nil {
			return nil, err
		}
		ob, err := a.readOriginal(ctx, rec, original)
		if err != nil || !ob.Running || ob.PendingJob || (ob.UnitFileState == "enabled") != rec.EnableOnBoot ||
			current == nil || current.Ref != rec.Plan.Original || current.Current.MainPID != ob.MainPID || current.Current.KernelStart != ob.KernelStart {
			view.Status.IssueCode = "activation_original_unknown"
			return view, nil
		}
		if rec.Plan.NativeOriginal != nil && a.runtime.RequireAbsent(ctx, original) != nil {
			view.Status.IssueCode = "activation_original_unknown"
			return view, nil
		}
		binding, err := (environments.ProgramStore{Home: a.home()}).Load(agent, rec.Plan.Candidate)
		if err != nil {
			return nil, err
		}
		version := ""
		if binding.Runtime != nil && binding.Runtime.Installation != nil && binding.Runtime.Installation.RegisteredProvenance.NPM != nil {
			version = binding.Runtime.Installation.RegisteredProvenance.NPM.Version
		}
		view.ProfileUntested = !rec.Plan.CandidateProfile.EnvironmentTested(version)
		view.CanSwitch = true
	} else if view.OriginalProgramChecked && !r.Operation.OriginalCleaned && slices.Contains([]environment.Phase{environment.Stopping, environment.OriginalDown, environment.Starting, environment.AwaitingHuman, environment.Accepted,
		environment.RollingBack, environment.CandidateDown, environment.Resuming}, r.Operation.Phase) && rec.State != "cancelled" {
		view.CanReturn = true
	}
	if !view.CanSwitch && !view.CanReturn && !view.CanConfirm {
		return view, nil
	}
	last, err := a.registry(rec)
	latest, readErr := loadActivation(a.home(), agent, op)
	child, childErr := a.runner.Store.Load(rec.Plan.DataOpID)
	if err != nil || readErr != nil || childErr != nil || environments.Digest(last) != environments.Digest(r) ||
		environments.Digest(latest) != environments.Digest(rec) || environments.Digest(child) != environments.Digest(j) {
		return nil, activationError("activation_stale")
	}
	if view.CanConfirm {
		// Human acceptance binds the actual candidate, not volatile HTTP
		// results or the independent fallback's readiness. Both remain visible.
		view.ConfirmationDigest = environments.Digest(struct {
			Record     *ActivationRecord
			Registry   environment.Registry
			Child      *revive.Journal
			Ref        environment.Ref
			Definition string
			Current    managedruntime.Observation
		}{rec, r, j, current.Ref, current.Definition.Digest(), current.Current})
	}
	view.Digest = environments.Digest(struct {
		Record   *ActivationRecord
		Registry environment.Registry
		Child    *revive.Journal
		Runtime  *ActivationReview
		Stage    string
		View     *environment.SwitchReview
	}{rec, r, j, q, stage, view})
	return view, ctx.Err()
}
