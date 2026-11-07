package server

import (
	"context"
	"encoding/json"

	"valise/porter/internal/sysfix"
)

// System settings Valise itself depends on (linger, time sync): a read on
// the daily key, and the audit entry of a fix the client ran on its own
// admin connection. Neither changes a system setting.
func init() {
	methods[sysfix.MethodStatus] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		return sysfix.Read(ctx, s.loginEnv(ctx), s.cfg.Home), nil
	}
	methods[sysfix.MethodAudit] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := decode[sysfix.AuditParams](raw)
		if err != nil {
			return nil, err
		}
		if err := sysfix.Audit(s.cfg.Home, p); err != nil {
			return nil, errInvalidParams
		}
		return struct{}{}, nil
	}
}
