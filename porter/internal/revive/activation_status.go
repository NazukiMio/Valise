package revive

import (
	"context"

	"valise/core/environment"
	"valise/core/identity"
	"valise/porter/internal/activeview"
	"valise/porter/internal/environments"
)

// SwitchStatus returns only observations. No receipt can start a service or
// restore a human confirmation, and an interrupted operation is never resumed.
func (a *ActivationRunner) SwitchStatus(ctx context.Context, agent identity.AgentRef, op string) (*environment.SwitchStatus, error) {
	if ctx == nil {
		return nil, activationError("activation_materials_unavailable")
	}
	rec, err := loadActivation(a.home(), agent, op)
	if err != nil {
		return nil, err
	}
	if historical, err := a.historicalSwitchStatus(ctx, rec); err != nil || historical != nil {
		return historical, err
	}
	q, err := a.Query(ctx, agent, op)
	if err != nil {
		return nil, err
	}
	r, err := a.registry(q.Record)
	if err != nil {
		return nil, err
	}
	v, _ := a.switchStatus(ctx, q, r)
	return v, ctx.Err()
}

func (a *ActivationRunner) switchStatus(ctx context.Context, q *ActivationReview, r environment.Registry) (*environment.SwitchStatus, *activeview.Binding) {
	rec := q.Record
	v := &environment.SwitchStatus{PreparationParams: environment.PreparationParams{Agent: rec.Plan.Agent, OperationID: rec.Plan.OperationID}, Snapshot: rec.Plan.Snapshot,
		Mode: r.Operation.Mode, Original: rec.Plan.Original, Candidate: rec.Plan.Candidate, State: rec.State, Phase: r.Operation.Phase,
		FailureCode: rec.FailureCode, IssueCode: q.IssueCode, OriginalNative: rec.Plan.NativeOriginal != nil, OriginalCleaned: r.Operation.OriginalCleaned}
	if rec.Cleanup != nil {
		v.CleanupIssueCode = rec.Cleanup.IssueCode
	}
	if v.Phase == environment.AwaitingHuman || v.Phase == environment.Accepted || v.Phase == environment.Cleaning {
		v.CleanupState = "retained"
		if v.Phase == environment.Cleaning || rec.Cleanup != nil && !rec.Cleanup.Complete {
			v.CleanupState = "pending"
		}
		if v.OriginalCleaned {
			v.CleanupState = "cleaned"
		}
	}
	if q.Native != nil {
		v.OriginalObserved, v.OriginalRunning, v.OriginalStopped = true, q.Native.Running, q.Native.Stopped
	}
	b, err := (activeview.Reader{Home: a.home(), Control: a.runtime}).Read(ctx, rec.Plan.Agent)
	if err != nil || b == nil || b.Ref != rec.Plan.Original && b.Ref != rec.Plan.Candidate || environments.Digest(b.Registry) != environments.Digest(r) {
		if v.IssueCode == "" {
			v.IssueCode = "activation_current_unverified"
		}
		return v, nil
	}
	v.Current = &b.Ref
	v.CurrentObserved, v.CurrentRunning, v.CurrentStopped = true, b.Current.Running, b.Current.Stopped
	v.GatewayObserved, v.PortOwned, v.HTTPResponded, v.GatewayPassed = b.Current.Running, b.Gateway.PortOwned, b.Gateway.HTTPResponded, b.Gateway.GatewayPassed
	v.ConversationKnown = b.ConversationConfirmed
	v.CanReviewOldCleanup = (v.Mode == environment.Compact || r.Mode == environment.Direct) && !v.OriginalCleaned && (v.Phase == environment.Accepted || v.Phase == environment.Cleaning) && b.Ref == rec.Plan.Candidate && b.ConversationConfirmed && (b.Current.Running || b.Current.Stopped) && !b.Current.PendingJob
	if b.Ref == rec.Plan.Original {
		v.OriginalObserved, v.OriginalRunning, v.OriginalStopped = true, b.Current.Running, b.Current.Stopped
	}
	return v, b
}
