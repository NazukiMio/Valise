package server

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"valise/core/login"
	"valise/core/vaultrpc"
	porterlogin "valise/porter/internal/login"
	"valise/porter/internal/seal"
)

func init() {
	methods[login.MethodPrepare] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := decode[login.PrepareParams](raw)
		if err != nil {
			return nil, err
		}
		return porterlogin.Prepare(s.loginEnv(ctx), p)
	}
	methods[login.MethodURLs] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := decode[login.SessionParams](raw)
		if err != nil {
			return nil, err
		}
		return porterlogin.URLs(s.cfg.Home, p.Session)
	}
	methods[login.MethodEnd] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := decode[login.EndParams](raw)
		if err != nil {
			return nil, err
		}
		sess, err := porterlogin.End(s.cfg.Home, p.Session)
		if sess != nil {
			// Only the login entry and exit code; never output or links.
			seal.Paths{Home: s.cfg.Home}.AppendAudit(seal.AuditEntry{Time: time.Now().UTC(), Op: "login", Instance: sess.InstanceID,
				Initiator: vaultrpc.InitiatorClient, Result: "exit " + strconv.Itoa(p.ExitCode), Reason: sess.LoginID})
		}
		return struct{}{}, err
	}
	methods[login.MethodListen] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := decode[login.ListenParams](raw)
		if err != nil {
			return nil, err
		}
		return porterlogin.ListenAddrs(p.Port)
	}
}
