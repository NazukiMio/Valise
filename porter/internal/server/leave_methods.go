package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"time"

	"valise/core/vaultrpc"
	"valise/porter/internal/protection"
	"valise/porter/internal/seal"
	"valise/porter/internal/timer"
)

// server_leave is the server side of removing a server and cleaning it
// (docs/ARCHITECTURE.md 2.3); the client runs the steps in order on its own
// admin connection. The backup library is never touched.
func init() {
	methods[vaultrpc.MethodServerLeave] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := decode[vaultrpc.ServerLeaveParams](raw)
		if err != nil {
			return nil, err
		}
		switch p.Step {
		case vaultrpc.LeaveTimers:
			done, err := timer.RemoveAll(ctx, s.loginEnv(ctx), s.cfg.Home)
			return vaultrpc.ServerLeaveResult{Timers: done}, err
		case vaultrpc.LeaveMark:
			return vaultrpc.ServerLeaveResult{}, seal.MarkServerRemoved(seal.Paths{Home: s.cfg.Home}, time.Now())
		case vaultrpc.LeavePorter:
			// Linux keeps a running program's file until it exits, so this
			// call can still answer.
			path := protection.DeployedPorter(s.cfg.Home)
			if err := os.Remove(path); errors.Is(err, os.ErrNotExist) {
				return vaultrpc.ServerLeaveResult{}, nil
			} else if err != nil {
				return nil, err
			}
			return vaultrpc.ServerLeaveResult{Porter: path}, nil
		}
		return nil, errInvalidParams
	}
}
