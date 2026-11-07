package server

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"valise/core/action"
	"valise/core/rpc"
	"valise/core/vaultrpc"
	"valise/porter/internal/actionjournal"
	"valise/porter/internal/actiontxn"
	"valise/porter/internal/seal"
)

func init() {
	methods[action.MethodPlan] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := decode[action.PlanParams](raw)
		if err != nil {
			return nil, err
		}
		if err = (actionjournal.Store{Home: s.cfg.Home}).Check(""); err != nil {
			return nil, err
		}
		return actiontxn.Plan(ctx, s.loginEnv(ctx), p.InstanceID, p.ActionID)
	}
	methods[action.MethodStatus] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := decode[action.OperationParams](raw)
		if err != nil {
			return nil, err
		}
		return (actionjournal.Store{Home: s.cfg.Home}).Load(p.OperationID)
	}
	methods[action.MethodResume] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := decode[action.ResumeParams](raw)
		if err != nil {
			return nil, err
		}
		j, err := (actionjournal.Store{Home: s.cfg.Home}).Load(p.OperationID)
		if err != nil {
			return nil, err
		}
		if err = s.require(rpc.Tier(j.Plan.Definition.AccessTier()), action.MethodResume); err != nil {
			return nil, err
		}
		result, err := (actiontxn.Runner{Env: s.loginEnv(ctx)}).Resume(ctx, p)
		outcome := "failed"
		if result != nil {
			outcome = fmt.Sprintf("exit %d", result.Exit)
		}
		(seal.Paths{Home: s.cfg.Home}).AppendAudit(seal.AuditEntry{Time: time.Now().UTC(), Op: action.MethodResume, Instance: j.Plan.InstanceID, Initiator: vaultrpc.InitiatorClient, Result: outcome, Tier: j.Plan.Definition.AccessTier(), OperationID: j.ID, Reason: p.Choice})
		return result, err
	}
	methods[action.MethodCleanup] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := decode[action.CleanupParams](raw)
		if err != nil {
			return nil, err
		}
		j, err := (actionjournal.Store{Home: s.cfg.Home}).Load(p.OperationID)
		if err != nil {
			return nil, err
		}
		// Listing leftovers is read-only. Actual deletion follows existing age-based tiers.
		tier := rpc.TierAdmin
		if p.Confirm {
			if j.Done && time.Since(j.Updated) >= sensitiveAge {
				tier = rpc.TierOwner
			}
			if err = s.require(tier, action.MethodCleanup); err != nil {
				return nil, err
			}
		}
		paths, err := (actiontxn.Runner{Env: s.loginEnv(ctx)}).Cleanup(ctx, p)
		if p.Confirm {
			outcome := "done"
			if err != nil {
				outcome = "failed"
			}
			(seal.Paths{Home: s.cfg.Home}).AppendAudit(seal.AuditEntry{Time: time.Now().UTC(), Op: action.MethodCleanup, Instance: j.Plan.InstanceID, Initiator: vaultrpc.InitiatorClient, Result: outcome, Tier: string(tier), OperationID: j.ID})
		}
		return map[string][]string{"paths": paths}, err
	}
}
