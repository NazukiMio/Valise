package revive

import (
	"context"
	"valise/core/profile"
	porterenroll "valise/porter/internal/enroll"
)

// HealthOnce checks declared probes without starting services or rolling back.
func HealthOnce(ctx context.Context, e *porterenroll.Env, p profile.Profile, unit string) bool {
	ok, _ := healthRound(ctx, e, p, unit)
	return ok
}
