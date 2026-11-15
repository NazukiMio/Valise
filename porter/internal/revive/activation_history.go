package revive

import (
	"context"

	"valise/core/environment"
	"valise/core/protection"
	porterenroll "valise/porter/internal/enroll"
	"valise/porter/internal/environments"
)

// Historical facts never inspect a reused directory or the old service
// definition. They describe a finished operation, not the current Agent.
func (a *ActivationRunner) historicalSwitchStatus(ctx context.Context, rec *ActivationRecord) (*environment.SwitchStatus, error) {
	r, err := (environments.Store{Home: a.home()}).Load(rec.Plan.Agent)
	if err != nil {
		return nil, err
	}
	if r.Operation != nil && r.Operation.ID == rec.Plan.OperationID {
		return nil, nil
	}
	if err := porterenroll.ValidateIdentity(ctx, a.home(), rec.Plan.Agent); err != nil {
		return nil, err
	}
	for _, old := range r.History {
		if old.ID != rec.Plan.OperationID {
			continue
		}
		if !environment.OperationFinished(old.Phase) || old.Snapshot != rec.Plan.Snapshot || old.Original != rec.Plan.Original || old.Candidate != rec.Plan.Candidate {
			return nil, activationError("activation_stale")
		}
		j, err := a.runner.Store.Load(rec.Plan.DataOpID)
		if err != nil {
			return nil, err
		}
		m := j.ManagedData
		digest, err := protection.Digest(j.Plan)
		if err != nil || m == nil || m.Agent != rec.Plan.Agent || m.OperationID != old.ID || m.Snapshot != old.Snapshot || m.Original != old.Original || m.Candidate != old.Candidate || m.PlanDigest != rec.DataPlanDigest || digest != m.PlanDigest || m.ManifestDigest != rec.DataManifestDigest {
			return nil, activationError("activation_stale")
		}
		last, err := (environments.Store{Home: a.home()}).Load(rec.Plan.Agent)
		if err != nil || environments.Digest(last) != environments.Digest(r) {
			return nil, activationError("activation_stale")
		}
		v := &environment.SwitchStatus{PreparationParams: environment.PreparationParams{Agent: rec.Plan.Agent, OperationID: old.ID},
			Snapshot: old.Snapshot, Mode: old.Mode, Original: old.Original, Candidate: old.Candidate, State: rec.State, Phase: old.Phase,
			FailureCode: rec.FailureCode, OriginalNative: rec.Plan.NativeOriginal != nil, OriginalCleaned: old.OriginalCleaned,
			Historical: true, HistoricalConfirmed: old.Confirmed, CleanupState: "superseded"}
		if old.OriginalCleaned {
			v.CleanupState = "cleaned"
		}
		return v, ctx.Err()
	}
	return nil, activationError("activation_stale")
}
