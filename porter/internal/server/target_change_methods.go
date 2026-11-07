package server

import (
	"context"
	"encoding/json"

	"valise/core/protection"
)

// Versioned change of a plan-referenced copy target (protection-plan/2).
// Prepare is daily and changes nothing; apply needs the owner tier.
func init() {
	methods[protection.MethodTargetChangePrepare] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		var p protection.TargetChangePrepareParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, errInvalidParams
		}
		return s.protectionStore(ctx).PrepareTargetChange(ctx, p)
	}
	methods[protection.MethodTargetChangeApply] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		var p protection.TargetChangeApplyParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, errInvalidParams
		}
		return s.protectionStore(ctx).ApplyTargetChange(ctx, p)
	}
}
