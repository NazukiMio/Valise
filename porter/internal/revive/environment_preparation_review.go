package revive

import (
	"context"
	"errors"
	"os"
	"slices"

	"valise/core/environment"
	"valise/core/identity"
	porterenroll "valise/porter/internal/enroll"
	"valise/porter/internal/environments"
	"valise/porter/internal/managedruntime"
)

// PreparationReview is a read-only projection. Condition flags do not confer
// management authority, acceptance or deletion permission.
type PreparationReview struct {
	Agent            identity.AgentRef                  `json:"agent"`
	OperationID      string                             `json:"operation_id"`
	Snapshot         identity.SnapshotRef               `json:"snapshot"`
	State            string                             `json:"state"`
	Mode             environment.Mode                   `json:"mode"`
	Phase            environment.Phase                  `json:"phase,omitempty"`
	FailureCode      string                             `json:"failure_code,omitempty"`
	IssueCode        string                             `json:"issue_code,omitempty"`
	Materials        string                             `json:"materials"`
	Probe            string                             `json:"probe"`
	Check            *environment.ProbeResult           `json:"check,omitempty"`
	Native           *managedruntime.Observation        `json:"native,omitempty"`
	Current          *managedruntime.Observation        `json:"current,omitempty"`
	OriginalManaged  *managedruntime.Observation        `json:"original_managed,omitempty"`
	Gateway          *managedruntime.GatewayObservation `json:"gateway,omitempty"`
	CanCancel        bool                               `json:"can_cancel"`
	CanReviewCleanup bool                               `json:"can_review_cleanup"`
	Retired          bool                               `json:"retired"`
}

func (r *Runner) ReviewEnvironmentPreparation(ctx context.Context, agent identity.AgentRef, op string) (*PreparationReview, error) {
	return r.reviewEnvironmentPreparation(ctx, agent, op, managedruntime.Controller{Home: r.Store.Home})
}
func (r *Runner) reviewEnvironmentPreparation(ctx context.Context, agent identity.AgentRef, op string, control managedruntime.Controller) (*PreparationReview, error) {
	p, err := loadPreparation(r.Store.Home, agent, op)
	if err != nil {
		return nil, err
	}
	v := &PreparationReview{Agent: agent, OperationID: op, Snapshot: identity.SnapshotRef{VaultID: p.Params.Input.VaultID, SnapshotID: p.Plan.SnapshotID}, State: p.State, Mode: p.Mode, FailureCode: p.FailureCode, Materials: "unknown", Probe: "unknown"}
	v.Check = p.Probe
	if p.State == "cleaned" {
		v.Materials, v.Probe, v.Retired = "cleared", "stopped", true
		return v, nil
	}
	if err := porterenroll.ValidateIdentity(ctx, r.Store.Home, agent); err != nil {
		v.IssueCode = "environment_prepare_changed"
		return v, nil
	}
	registry, regErr := (environments.Store{Home: r.Store.Home}).Load(agent)
	if regErr != nil && !errors.Is(regErr, os.ErrNotExist) {
		v.IssueCode = "environment_prepare_changed"
		return v, nil
	}
	if registry.Operation != nil {
		if registry.Operation.ID != op {
			for _, old := range registry.History {
				if old.ID == op && old.Snapshot == v.Snapshot && old.Original == p.Original && old.Candidate == p.Candidate && old.Mode == p.Mode && environment.OperationFinished(old.Phase) {
					v.State, v.Phase, v.Retired = "historical", old.Phase, true
					return v, nil
				}
			}
			v.IssueCode = "environment_prepare_changed"
			return v, nil
		}
		v.Phase = registry.Operation.Phase
	}
	stamp := environments.Digest(registry)
	if p.ProbeWorkspace == "" && p.Probe == nil && p.State != "probing" {
		v.Probe = "not_started"
	}
	leases, err := holdPreparationProbe(r.Store.Home, p)
	if err == nil {
		closePreparationLeases(leases)
		if v.Probe != "not_started" {
			v.Probe = "stopped"
		}
	}
	if items, err := r.preparationCleanupItemsContext(ctx, p); err == nil {
		v.Materials = "none"
		for _, item := range items {
			if item.Present {
				v.Materials = "retained"
				break
			}
		}
	}
	if activation, err := loadActivation(r.Store.Home, agent, op); err == nil {
		a := NewBoundActivationRunner(r)
		a.runtime = control
		review, err := a.Query(ctx, agent, op)
		if err == nil {
			v.Native, v.Current, v.Gateway, v.IssueCode = review.Native, review.Current, review.Gateway, review.IssueCode
		}
		v.CanCancel = slices.Contains([]string{"ready", "needs_review"}, activation.State) && len(activation.Facts) == 0
	} else if !errors.Is(err, os.ErrNotExist) {
		v.IssueCode = "environment_prepare_changed"
		return v, nil
	} else {
		v.CanCancel = true
	}
	native, _, err := r.preparationUnchanged(ctx, p, control)
	if err == nil {
		if p.ManagedSource != nil {
			if current, err := r.managedPreparationUnchanged(ctx, p, control, false); err == nil {
				v.Current = &current.Current
				v.OriginalManaged = &current.Current
			}
		} else if ob, err := control.ReadNative(ctx, native); err == nil {
			v.Native = &ob
		}
		v.CanCancel = v.CanCancel && !slices.Contains([]string{"cancelled", "cleaning"}, p.State) && v.Probe != "unknown"
		v.CanReviewCleanup = slices.Contains([]string{"cancelled", "cleaning"}, p.State) && v.Probe != "unknown" && v.Materials != "unknown"
	} else {
		v.CanCancel = false
		if v.IssueCode == "" && (v.Phase == "" || v.Phase == environment.Building || v.Phase == environment.Prepared || v.Phase == environment.Cancelled) {
			v.IssueCode = activationFailureCode(err)
		}
	}
	final, err := loadPreparation(r.Store.Home, agent, op)
	last, lastErr := (environments.Store{Home: r.Store.Home}).Load(agent)
	if err != nil || environments.Digest(final) != environments.Digest(p) || (lastErr != nil && !errors.Is(lastErr, os.ErrNotExist)) || environments.Digest(last) != stamp {
		v.Native, v.Current, v.Gateway = nil, nil, nil
		v.CanCancel, v.CanReviewCleanup = false, false
		v.Materials, v.Probe, v.IssueCode = "unknown", "unknown", "environment_prepare_changed"
	}
	return v, nil
}
