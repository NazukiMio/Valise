package server

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"valise/core/action"
	"valise/core/rpc"
	"valise/core/valise"
	"valise/core/vaultrpc"
	porteraction "valise/porter/internal/action"
	porterenroll "valise/porter/internal/enroll"
	"valise/porter/internal/seal"
)

func init() {
	methods[action.MethodRun] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		var p action.RunParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, errInvalidParams
		}
		prof, err := porterenroll.ReadProfile(s.cfg.Home, p.InstanceID)
		if err != nil {
			return nil, err
		}
		a := prof.FindAction(p.ActionID)
		if a == nil {
			return nil, valise.New("action_unknown", "服务器上接入时保存的 profile 里没有动作 "+p.ActionID,
				"profile 更新后需要重新接入一次才能使用新动作：valise enroll <服务器> --accept")
		}
		if err := s.require(rpc.Tier(a.AccessTier()), action.MethodRun+" "+a.ID); err != nil {
			return nil, err
		}
		res, err := porteraction.Run(ctx, s.loginEnv(ctx).For(prof), a)
		result := "failed"
		if res != nil {
			result = fmt.Sprintf("exit %d", res.Exit)
		}
		reason := fmt.Sprintf("%s (%s)", a.ID, a.Risk)
		if p.Sealed {
			reason += "; escape pod first"
		}
		// The output and any secret stay out of the audit.
		seal.Paths{Home: s.cfg.Home}.AppendAudit(seal.AuditEntry{Time: time.Now().UTC(), Op: "action", Instance: p.InstanceID,
			Initiator: vaultrpc.InitiatorClient, Result: result, Reason: reason})
		return res, err
	}
}
