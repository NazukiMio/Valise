package service

import (
	"context"
	"valise/core/revive"
)

// Rebuilding recorded programs or installing their service still requires a
// separate management login; proving ownership on a daily channel is not it.
func restoreNeedsAdmin(plan revive.Plan) bool {
	return plan.Body.Action == revive.BodyNPMInstall || plan.InstallService
}

func (s *Service) restoreOperationPorter(ctx context.Context, host string, plan revive.Plan) (*ownerPorter, func(), error) {
	if !restoreNeedsAdmin(plan) {
		o, err := s.ownerPorter(ctx, host)
		return o, func() {}, err
	}
	transport, done, err := s.adminTransport(ctx, host)
	if err != nil {
		return nil, func() {}, err
	}
	found, err := s.porterOn(ctx, transport, false)
	if err != nil {
		done()
		return nil, func() {}, err
	}
	return &ownerPorter{host: host, t: transport, path: found.Path}, done, nil
}
func (s *Service) callRestoreOperation(ctx context.Context, host string, plan revive.Plan, env []string, method string, params, result any) error {
	porter, done, err := s.restoreOperationPorter(ctx, host, plan)
	if err != nil {
		return err
	}
	defer done()
	return s.callOwner(ctx, porter, env, method, params, result)
}
