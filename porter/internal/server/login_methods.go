package server

import (
	"context"
	"encoding/json"
	"strconv"
	"time"
	"valise/porter/internal/forced"

	"valise/core/install"
	"valise/core/login"
	"valise/core/vaultrpc"
	porterinstall "valise/porter/internal/install"
	porterlogin "valise/porter/internal/login"
	"valise/porter/internal/seal"
)

func init() {
	methods[install.MethodNativeInfo] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := decode[install.NativeInfoParams](raw)
		if err != nil {
			return nil, err
		}
		info, err := porterinstall.NativeInfo(s.cfg.Home, p.InstanceID)
		if err != nil {
			return nil, err
		}
		info.Service = porterinstall.NativeServices(ctx, s.loginEnv(ctx), s.cfg.Home, p.InstanceID)
		return info, nil
	}
	methods[login.MethodPrepare] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := decode[login.PrepareParams](raw)
		if err != nil {
			return nil, err
		}
		res, err := porterlogin.Prepare(s.loginEnv(ctx), p)
		if err != nil || !s.daily() {
			return res, err
		}
		// The daily key runs login-run only with this one-time grant.
		res.Grant, err = forced.Mint(s.cfg.Home, "login-run", res.Session)
		return res, err
	}
	methods[login.MethodEntryAuth] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := decode[login.EntryAuthParams](raw)
		if err != nil {
			return nil, err
		}
		lock, unlock, err := (seal.Paths{Home: s.cfg.Home}).ActionLock("")
		if err != nil {
			return nil, err
		}
		defer unlock()
		return porterlogin.EntryAuthWithLock(ctx, s.loginEnv(ctx), s.cfg.Home, p, lock)
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
