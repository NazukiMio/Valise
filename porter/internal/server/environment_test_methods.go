package server

import (
	"context"
	"encoding/json"

	"valise/core/environment"
	"valise/core/revive"
	"valise/core/rpc"
)

func init() {
	methods[environment.MethodTest] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		if err := s.require(rpc.TierAdmin, environment.MethodTest); err != nil {
			return nil, err
		}
		p, err := decode[revive.OpParams](raw)
		if err != nil {
			return nil, err
		}
		return s.runner(ctx).TestEnvironment(ctx, p.OpID)
	}
}
