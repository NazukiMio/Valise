package managedruntime

import (
	"context"
	"valise/core/profile"
)

// DisableStartup precedes any stop/exchange intent. An interrupted exchange
// must not boot an older programme definition against a newer data directory.
func (c Controller) DisableStartup(ctx context.Context, d Definition) (ActionResult, error) {
	return c.startup(ctx, d, false, profile.Profile{})
}

// EnableStartup rechecks owned HTTP readiness itself. The activation journal
// additionally binds this action to the current Agent/slot revision. This is
// not human dialogue acceptance and does not authorize cleaning the old slot.
func (c Controller) EnableStartup(ctx context.Context, d Definition, p profile.Profile) (ActionResult, error) {
	return c.startup(ctx, d, true, p)
}

func (c Controller) startup(ctx context.Context, d Definition, enable bool, p profile.Profile) (ActionResult, error) {
	unlock, err := c.lock(d)
	if err != nil {
		return ActionResult{}, err
	}
	defer unlock()
	before, err := c.Read(ctx, d)
	if err != nil {
		return ActionResult{}, err
	}
	result := ActionResult{Observed: before}
	verb, wanted := "disable", "disabled"
	if enable {
		proof, err := c.CheckHTTP(ctx, d, p)
		if err != nil || !proof.GatewayPassed {
			return result, ErrState
		}
		verb, wanted = "enable", "enabled"
	}
	if before.UnitFileState == wanted {
		return result, nil
	}
	_, code, commandErr := c.call(ctx, verb, "--", d.name)
	result.Dispatched, result.CommandExited, result.ExitCode = true, commandErr == nil, code
	result.Observed, err = c.Read(ctx, d)
	if commandErr != nil || code != 0 {
		return result, ErrAction
	}
	if err != nil || result.Observed.UnitFileState != wanted {
		return result, ErrState
	}
	return result, nil
}
