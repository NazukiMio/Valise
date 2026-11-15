package revive

import (
	"context"

	"valise/core/environment"
	"valise/core/identity"
	"valise/core/valise"
	"valise/porter/internal/seal"
)

// ActivateReviewed compares fresh facts while holding the very same lock as
// activation. The digest is neither a source of executable facts nor a grant.
func (a *ActivationRunner) ActivateReviewed(ctx context.Context, agent identity.AgentRef, op, expected string, allowUntested bool) (*ActivationReview, error) {
	if ctx == nil {
		return nil, activationError("activation_materials_unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if expected == "" {
		return nil, activationError("activation_stale")
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
		return a.Query(ctx, agent, op) // Includes interrupted intents; never replay.
	}
	review, err := a.reviewSwitchLocked(ctx, agent, op)
	if err != nil {
		return nil, err
	}
	if !review.CanSwitch || review.Digest != expected {
		return nil, activationError("activation_stale")
	}
	if review.ProfileUntested && !allowUntested {
		return nil, valise.New("environment_profile_untested", "此版本的并行恢复尚未完成实际验收，没有切换", "查看此版本的影响，并明确允许未核实能力后再操作")
	}
	return a.activateLocked(ctx, rec)
}

func (a *ActivationRunner) ReturnReviewed(ctx context.Context, agent identity.AgentRef, op, expected string) (*ActivationReview, error) {
	if ctx == nil {
		return nil, activationError("activation_materials_unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if expected == "" {
		return nil, activationError("activation_stale")
	}
	unlock, err := (seal.Paths{Home: a.home()}).LockFor("environment_return")
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
	if r.Operation.Phase == environment.RolledBack {
		return a.Query(ctx, agent, op)
	}
	review, err := a.reviewSwitchLocked(ctx, agent, op)
	if err != nil {
		return nil, err
	}
	if !review.CanReturn || review.Digest != expected {
		return nil, activationError("activation_stale")
	}
	return a.returnReviewedLocked(ctx, agent, op)
}
