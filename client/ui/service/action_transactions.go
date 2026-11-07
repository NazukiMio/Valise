package service

import (
	"context"
	"encoding/json"
	"slices"

	"valise/core/action"
	"valise/core/enroll"
	"valise/core/rpc"
	"valise/core/valise"
)

func transactionCompatibility(hello *rpc.HelloResult, method string, params any) error {
	if err := n2Compatibility(hello, method, params); err != nil {
		return err
	}
	required := method == action.MethodPlan || method == action.MethodStatus || method == action.MethodResume || method == action.MethodCleanup
	switch method {
	case action.MethodRun:
		var p action.RunParams
		b, err := json.Marshal(params)
		if err != nil {
			return err
		}
		if err = json.Unmarshal(b, &p); err != nil {
			return err
		}
		required = p.PlanDigest != ""
	case enroll.MethodInstanceWrite:
		var p enroll.InstanceWriteParams
		b, err := json.Marshal(params)
		if err != nil {
			return err
		}
		if err = json.Unmarshal(b, &p); err != nil {
			return err
		}
		for _, a := range p.Profile.Actions {
			if a.Transaction != nil || a.Steps != nil {
				required = true
			}
		}
	}
	if required && (hello == nil || !slices.Contains(hello.Capabilities, action.CapabilityTransactions)) {
		return valise.New("action_porter_old", "服务器 porter 尚不支持配置事务，操作没有发送", "先更新服务器上的 porter，再重新确认动作")
	}
	return nil
}

func (s *Service) PlanAction(ctx context.Context, host, instance, id string) (*action.Plan, error) {
	var plan action.Plan
	err := s.withSession(ctx, host, func(c caller) error {
		return c.Call(ctx, action.MethodPlan, action.PlanParams{InstanceID: instance, ActionID: id}, &plan)
	})
	return &plan, err
}
func (s *Service) ActionStatus(ctx context.Context, host, op string) (*action.Operation, error) {
	var operation action.Operation
	err := s.withSession(ctx, host, func(c caller) error {
		return c.Call(ctx, action.MethodStatus, action.OperationParams{OperationID: op}, &operation)
	})
	return &operation, err
}
func (s *Service) ResumeAction(ctx context.Context, host, op, choice string) (*ActionResult, error) {
	status, err := s.ActionStatus(ctx, host, op)
	if err != nil {
		return nil, err
	}
	if err = s.RequireGate(ctx, host, status.Plan.InstanceID); err != nil {
		return nil, err
	}
	var result ActionResult
	err = s.withSession(ctx, host, func(c caller) error {
		return c.Call(ctx, action.MethodResume, action.ResumeParams{OperationID: op, Choice: choice}, &result.RunResult)
	})
	return &result, err
}
func (s *Service) CleanupAction(ctx context.Context, host, op string, confirm bool) ([]string, error) {
	var result struct {
		Paths []string `json:"paths"`
	}
	err := s.withSession(ctx, host, func(c caller) error {
		return c.Call(ctx, action.MethodCleanup, action.CleanupParams{OperationID: op, Confirm: confirm}, &result)
	})
	return result.Paths, err
}
func (s *Service) PendingActions(ctx context.Context, host string) ([]action.PendingAction, error) {
	session, err := s.session(ctx, host)
	if err != nil {
		return nil, err
	}
	defer session.Close()
	return session.hello.PendingActions, nil
}
