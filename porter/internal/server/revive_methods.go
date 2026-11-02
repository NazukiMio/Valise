package server

import (
	"context"
	"encoding/json"
	"time"

	"valise/core/revive"
	"valise/core/rpc"
	"valise/core/vault"
	"valise/core/vaultrpc"
	"valise/porter/internal/forced"
	porterrevive "valise/porter/internal/revive"
	"valise/porter/internal/seal"
)

func (s *session) runner(ctx context.Context) *porterrevive.Runner {
	p := seal.Paths{Home: s.cfg.Home}
	return &porterrevive.Runner{Env: s.loginEnv(ctx), Store: porterrevive.Store{Home: s.cfg.Home},
		Audit: func(op, instance, result string) {
			p.AppendAudit(seal.AuditEntry{Time: time.Now().UTC(), Op: "revive", Instance: instance, Initiator: vaultrpc.InitiatorClient, Result: result, Reason: op})
		}}
}

func decode[T any](raw json.RawMessage) (T, error) {
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		return v, errInvalidParams
	}
	return v, nil
}

func init() {
	store := func(s *session) porterrevive.Store { return porterrevive.Store{Home: s.cfg.Home} }
	methods[revive.MethodInspect] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := decode[revive.InspectParams](raw)
		if err != nil {
			return nil, err
		}
		return porterrevive.Inspect(ctx, s.loginEnv(ctx), store(s), p)
	}
	methods[revive.MethodStageBegin] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := decode[revive.StageBeginParams](raw)
		if err != nil {
			return nil, err
		}
		j, err := porterrevive.StageBegin(ctx, s.loginEnv(ctx), store(s), p.Plan)
		if err != nil || !s.daily() {
			return j, err
		}
		// The daily key runs stage-write only with this one-time grant.
		grant, err := forced.Mint(s.cfg.Home, "stage-write", j.Plan.OpID)
		return struct {
			*revive.Journal
			Grant string `json:"grant"`
		}{j, grant}, err
	}
	methods[revive.MethodStageVerify] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := decode[revive.StageVerifyParams](raw)
		if err != nil {
			return nil, err
		}
		return porterrevive.StageVerify(store(s), p)
	}
	methods[revive.MethodSwap] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := decode[revive.OpParams](raw)
		if err != nil {
			return nil, err
		}
		// The swap must not be interrupted by the client going away.
		return s.runner(ctx).Swap(context.WithoutCancel(ctx), p.OpID)
	}
	methods[revive.MethodResume] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := decode[revive.ResumeParams](raw)
		if err != nil {
			return nil, err
		}
		return s.runner(ctx).Resume(context.WithoutCancel(ctx), p)
	}
	methods[revive.MethodJournal] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := decode[revive.OpParams](raw)
		if err != nil {
			return nil, err
		}
		return store(s).Load(p.OpID)
	}
	methods[revive.MethodPrevList] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		return porterrevive.PrevList(store(s))
	}
	methods[revive.MethodPrevDrop] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := decode[revive.OpParams](raw)
		if err != nil {
			return nil, err
		}
		tier := rpc.TierAdmin
		if made, err := porterrevive.Finished(store(s), p.OpID); err == nil && time.Since(made) >= sensitiveAge {
			tier = rpc.TierOwner
		}
		if err := s.require(tier, revive.MethodPrevDrop); err != nil {
			return nil, err
		}
		removed, err := porterrevive.PrevDrop(store(s), p.OpID)
		return map[string][]string{"removed": removed}, err
	}
	methods[revive.MethodOldList] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		return porterrevive.OldList(store(s))
	}
	methods[revive.MethodOldCleanup] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := decode[revive.OldCleanupParams](raw)
		if err != nil {
			return nil, err
		}
		removed, err := porterrevive.OldCleanup(store(s), p.InstanceID)
		return map[string][]string{"removed": removed}, err
	}
	methods[revive.MethodService] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := decode[revive.ServiceParams](raw)
		if err != nil {
			return nil, err
		}
		e := s.loginEnv(ctx)
		if p.Action == "stop" {
			return struct{}{}, porterrevive.StopUnit(ctx, e, p.Unit)
		}
		return struct{}{}, porterrevive.StartUnit(ctx, e, p.Unit)
	}
	methods[revive.MethodRecipientRm] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := decode[revive.RecipientRemoveParams](raw)
		if err != nil {
			return nil, err
		}
		removed, all, err := seal.RemoveRecipient(seal.Paths{Home: s.cfg.Home}, p.Recipient)
		if err != nil {
			return nil, err
		}
		res := vaultrpc.RecipientAddResult{Added: removed, Fingerprints: []string{}}
		for _, r := range all {
			res.Fingerprints = append(res.Fingerprints, vault.Fingerprint(r))
		}
		return res, nil
	}
}
