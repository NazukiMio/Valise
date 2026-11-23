package server

import (
	"context"
	"encoding/json"

	"valise/core/rpc"
	"valise/porter/internal/seal"
)

// These handlers mutate an installation without taking the vault lock themselves.
// Seal/sync/deletions already use Paths.Lock; action transactions own their lock.
var installationMutations = map[string]bool{
	"instance_write": true, "instance_forget": true, "entry_port_set": true, "agent_label_set": true,
	"stage_begin": true, "stage_verify": true, "swap": true, "revive_resume": true,
	"revive_review": true, "revive_resume_checked": true,
	"service_control": true, "kit_install": true, "kit_uninstall": true,
	"kit_tool_begin": true, "kit_tool_remove": true, "proposals_decide": true,
	"login_prepare": true, "login_end": true,
	"prev_drop": true, "old_cleanup": true,
}

func (s *session) guardedCall(ctx context.Context, req rpc.Request, h func(*session, context.Context, json.RawMessage) (any, error)) (any, error) {
	if installationMutations[req.Method] {
		unlock, err := (seal.Paths{Home: s.cfg.Home}).LockFor(req.Method)
		if err != nil {
			return nil, err
		}
		defer unlock()
	}
	return h(s, ctx, req.Params)
}
