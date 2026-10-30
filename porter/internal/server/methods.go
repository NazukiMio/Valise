package server

import (
	"context"
	"encoding/json"

	"valise/core/enroll"
	"valise/core/rpc"
	"valise/core/valise"
	"valise/core/vault"
	"valise/core/vaultrpc"
	porterenroll "valise/porter/internal/enroll"
	"valise/porter/internal/seal"
)

// session holds per-connection state.
type session struct {
	cfg Config
	env *porterenroll.Env
}

// loginEnv reads the login environment once per session.
func (s *session) loginEnv(ctx context.Context) *porterenroll.Env {
	if s.env == nil {
		if s.cfg.LoadEnv != nil {
			s.env = s.cfg.LoadEnv(ctx, s.cfg.Home)
		} else {
			s.env = porterenroll.LoadEnv(ctx, s.cfg.Home)
		}
	}
	return s.env
}

type handler func(s *session, ctx context.Context, params json.RawMessage) (any, error)

var methods = map[string]handler{
	enroll.MethodDetect: func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		var p enroll.DetectParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, errInvalidParams
		}
		return porterenroll.Detect(ctx, s.loginEnv(ctx), p.Profiles), nil
	},
	enroll.MethodScan: func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		var p enroll.ScanParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, errInvalidParams
		}
		prev, err := porterenroll.ReadRecord(s.cfg.Home, p.InstanceID)
		if err != nil {
			return nil, err
		}
		return porterenroll.Scan(ctx, s.loginEnv(ctx), p, prev)
	},
	enroll.MethodInstanceGet: func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		var p enroll.InstanceGetParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, errInvalidParams
		}
		r, err := porterenroll.ReadRecord(s.cfg.Home, p.InstanceID)
		return enroll.InstanceGetResult{Record: r}, err
	},
	enroll.MethodInstanceWrite: func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		var p enroll.InstanceWriteParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, errInvalidParams
		}
		path, err := porterenroll.WriteRecord(s.cfg.Home, p.Record, p.Profile)
		return enroll.InstanceWriteResult{Path: path}, err
	},
}

func init() {
	paths := func(s *session) seal.Paths { return seal.Paths{Home: s.cfg.Home} }
	methods[vaultrpc.MethodRecipientAdd] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		var p vaultrpc.RecipientAddParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, errInvalidParams
		}
		added, all, err := seal.AddRecipient(paths(s), p.Recipient, p.Label)
		if err != nil {
			return nil, err
		}
		res := vaultrpc.RecipientAddResult{Added: added, Fingerprints: []string{}}
		for _, r := range all {
			res.Fingerprints = append(res.Fingerprints, vault.Fingerprint(r))
		}
		return res, nil
	}
	methods[vaultrpc.MethodSeal] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		var p vaultrpc.SealParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, errInvalidParams
		}
		return seal.Seal(ctx, s.loginEnv(ctx), paths(s), p, vaultrpc.InitiatorClient)
	}
	methods[vaultrpc.MethodStatus] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		return seal.Status(paths(s))
	}
	methods[vaultrpc.MethodVerify] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		return seal.Verify(paths(s))
	}
	methods[vaultrpc.MethodFiles] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		return seal.Files(paths(s))
	}
	methods[vaultrpc.MethodTargetSet] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		var p vaultrpc.TargetSetParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, errInvalidParams
		}
		return struct{}{}, seal.SetTarget(paths(s), p.Target)
	}
	methods[vaultrpc.MethodSync] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		var p vaultrpc.SyncParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, errInvalidParams
		}
		return seal.Sync(ctx, s.loginEnv(ctx), paths(s), p.Target, vaultrpc.InitiatorClient)
	}
}

var errInvalidParams = &rpc.Error{Code: rpc.CodeInvalidParams, Message: "invalid params"}

func (s *session) call(ctx context.Context, req rpc.Request) rpc.Response {
	h, ok := methods[req.Method]
	if !ok {
		return errorResponse(req.ID, rpc.CodeMethodNotFound, "method not found: "+req.Method, nil)
	}
	res, err := h(s, ctx, req.Params)
	if err != nil {
		if re, ok := err.(*rpc.Error); ok {
			return errorResponse(req.ID, re.Code, re.Message, re.Data)
		}
		ue, ok := valise.As(err)
		if !ok {
			ue = valise.New("porter_internal", "服务器上的 porter 执行失败", "加上 -v 查看细节，并向项目报告").WithDetail(err.Error())
		}
		return errorResponse(req.ID, rpc.CodeUserError, ue.Code, ue)
	}
	b, err := json.Marshal(res)
	if err != nil {
		return errorResponse(req.ID, rpc.CodeInternalError, err.Error(), nil)
	}
	return rpc.Response{JSONRPC: rpc.JSONRPCVersion, ID: req.ID, Result: b}
}
