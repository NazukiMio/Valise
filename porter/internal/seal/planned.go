package seal

import (
	"context"

	"valise/core/vaultrpc"
	porterenroll "valise/porter/internal/enroll"
)

// SealPlanned performs the existing fixed seal, including its vault/B03 lock,
// without invoking legacy targets.auto. The accepted plan runner owns copying.
func SealPlanned(ctx context.Context, e *porterenroll.Env, p Paths, params vaultrpc.SealParams, initiator string) (*vaultrpc.SealReport, error) {
	return sealLocked(ctx, e, p, params, initiator)
}
