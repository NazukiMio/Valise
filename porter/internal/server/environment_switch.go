package server

import (
	"context"
	"encoding/json"
	"time"

	"valise/core/environment"
	"valise/core/rpc"
	porterrevive "valise/porter/internal/revive"
)

func init() {
	methods[environment.MethodConfirm] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		if err := s.require(rpc.TierAdmin, environment.MethodConfirm); err != nil {
			return nil, err
		}
		var p environment.ReviewedOperationParams
		if strictPreparationDecode(raw, &p) != nil || !preparationReference(p.Agent, p.OperationID) || !preparationHash(p.Digest) {
			return nil, errInvalidParams
		}
		a := porterrevive.NewBoundActivationRunner(s.runner(ctx))
		if _, err := a.ConfirmReviewed(ctx, p.Agent, p.OperationID, p.Digest); err != nil {
			return nil, err
		}
		query, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancel()
		return a.SwitchStatus(query, p.Agent, p.OperationID)
	}
	methods[environment.MethodSwitchReview] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := preparationParams(raw)
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()
		return porterrevive.NewBoundActivationRunner(s.runner(ctx)).ReviewSwitch(ctx, p.Agent, p.OperationID)
	}
	methods[environment.MethodSwitchStatus] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := preparationParams(raw)
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()
		return porterrevive.NewBoundActivationRunner(s.runner(ctx)).SwitchStatus(ctx, p.Agent, p.OperationID)
	}
	methods[environment.MethodSwitch] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		if err := s.require(rpc.TierAdmin, environment.MethodSwitch); err != nil {
			return nil, err
		}
		var p environment.SwitchParams
		if strictPreparationDecode(raw, &p) != nil || !preparationReference(p.Agent, p.OperationID) || !preparationHash(p.Digest) {
			return nil, errInvalidParams
		}
		a := porterrevive.NewBoundActivationRunner(s.runner(ctx))
		if _, err := a.ActivateReviewed(ctx, p.Agent, p.OperationID, p.Digest, p.AllowUntested); err != nil {
			return nil, err
		}
		query, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancel()
		return a.SwitchStatus(query, p.Agent, p.OperationID)
	}
	methods[environment.MethodReturn] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		if err := s.require(rpc.TierAdmin, environment.MethodReturn); err != nil {
			return nil, err
		}
		var p environment.ReviewedOperationParams
		if strictPreparationDecode(raw, &p) != nil || !preparationReference(p.Agent, p.OperationID) || !preparationHash(p.Digest) {
			return nil, errInvalidParams
		}
		a := porterrevive.NewBoundActivationRunner(s.runner(ctx))
		if _, err := a.ReturnReviewed(ctx, p.Agent, p.OperationID, p.Digest); err != nil {
			return nil, err
		}
		query, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancel()
		return a.SwitchStatus(query, p.Agent, p.OperationID)
	}
}
