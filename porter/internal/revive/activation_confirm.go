package revive

import (
	"context"

	"valise/core/environment"
	"valise/core/identity"
	"valise/porter/internal/seal"
)

// ConfirmReviewed records an explicit human action for this candidate. It
// reads no conversation, sends no message and performs no service, data
// exchange or cleanup action. Only private confirmation metadata is written.
func (a *ActivationRunner) ConfirmReviewed(ctx context.Context, agent identity.AgentRef, op, expected string) (*ActivationReview, error) {
	if ctx == nil {
		return nil, activationError("activation_materials_unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if expected == "" {
		return nil, activationError("activation_stale")
	}
	previous, err := loadActivation(a.home(), agent, op)
	if err != nil {
		return nil, err
	}
	known, err := a.registry(previous)
	if err != nil {
		return nil, err
	}
	if known.Operation.Phase == environment.Accepted && known.Operation.Confirmed {
		return a.Query(ctx, agent, op)
	}
	unlock, err := (seal.Paths{Home: a.home()}).LockFor("environment_confirm")
	if err != nil {
		return nil, err
	}
	defer unlock()
	rec, err := loadActivation(a.home(), agent, op)
	if err != nil {
		return nil, err
	}
	r, err := a.registry(rec)
	if err != nil {
		return nil, err
	}
	if r.Operation.Phase == environment.Accepted && r.Operation.Confirmed {
		return a.Query(ctx, agent, op) // A lost acknowledgement never repeats acceptance.
	}
	review, err := a.reviewSwitchLocked(ctx, agent, op)
	if err != nil {
		return nil, err
	}
	if !review.CanConfirm || review.ConfirmationDigest != expected {
		return nil, activationError("activation_stale")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := a.fact(rec, "human_acceptance", func(*ActivationFact) error {
		return a.transition(rec, func(r environment.Registry, id string) (environment.Registry, error) {
			return r.ConfirmUsable(id, rec.Plan.Candidate)
		})
	}); err != nil {
		return nil, err
	}
	return a.queryAfter(ctx, agent, op)
}
