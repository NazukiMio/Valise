package server

import (
	"context"
	"encoding/json"
	"time"

	"valise/core/environment"
	"valise/core/protection"
	"valise/core/rpc"
	porterrevive "valise/porter/internal/revive"
)

func init() {
	methods[environment.MethodCurrentOldCleanup] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		var p environment.SettingsParams
		if strictPreparationDecode(raw, &p) != nil || protection.ValidateAgent(p.Agent) != nil {
			return nil, errInvalidParams
		}
		ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()
		return porterrevive.NewBoundActivationRunner(s.runner(ctx)).ReviewCurrentOldCleanup(ctx, p.Agent)
	}
	methods[environment.MethodOldCleanupReview] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := preparationParams(raw)
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()
		return porterrevive.NewBoundActivationRunner(s.runner(ctx)).ReviewOldCleanup(ctx, p.Agent, p.OperationID)
	}
	methods[environment.MethodOldCleanup] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		if err := s.require(rpc.TierAdmin, environment.MethodOldCleanup); err != nil {
			return nil, err
		}
		var p environment.ReviewedOperationParams
		if strictPreparationDecode(raw, &p) != nil || !preparationReference(p.Agent, p.OperationID) || !preparationHash(p.Digest) {
			return nil, errInvalidParams
		}
		ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
		defer cancel()
		return porterrevive.NewBoundActivationRunner(s.runner(ctx)).CleanupOldReviewed(ctx, p.Agent, p.OperationID, p.Digest)
	}
}
