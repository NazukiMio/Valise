package server

import (
	"context"
	"encoding/json"
	"valise/core/environment"
	"valise/core/revive"
)

func init() {
	methods[environment.MethodPlan] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := decode[revive.EnvironmentPlanParams](raw)
		if err != nil {
			return nil, err
		}
		return s.runner(ctx).PlanEnvironment(ctx, p)
	}
}
