// Package server implements `valise-porter rpc`: JSON-RPC 2.0 over stdio.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"runtime"

	"valise/core/action"
	"valise/core/identity"
	coreinstall "valise/core/install"
	"valise/core/protection"
	corerevive "valise/core/revive"
	"valise/core/rpc"
	"valise/core/valise"
	"valise/core/vaultrpc"
	"valise/porter/internal/actionjournal"
	porterenroll "valise/porter/internal/enroll"
	porterrevive "valise/porter/internal/revive"
	"valise/porter/internal/sysfix"
	"valise/porter/internal/tools"
)

// Config is what the server needs from its environment.
type Config struct {
	Version string
	Home    string
	// DetectTools defaults to tools.Detect on the login PATH.
	DetectTools func(context.Context) []rpc.Tool
	// LoadEnv defaults to enroll.LoadEnv; tests replace it.
	LoadEnv func(context.Context, string) *porterenroll.Env
	// Tier is the connection's access level; TierDaily refuses admin
	// methods (docs/ARCHITECTURE.md 2.1). Empty means a full login.
	Tier rpc.Tier
}

// JournalDir is the swap journal directory under HOME.
func JournalDir(home string) string {
	return filepath.Join(home, ".local", "state", "valise", "journal")
}

// Serve answers requests until r reaches EOF. The first request must be
// hello.
func Serve(ctx context.Context, r io.Reader, w io.Writer, cfg Config) error {
	conn := rpc.NewConn(r, w)
	callCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	defer close(done)
	incoming := readAhead(done, conn, cancel)
	helloed := false
	s := &session{cfg: cfg}
	if cfg.DetectTools == nil {
		cfg.DetectTools = func(ctx context.Context) []rpc.Tool {
			return tools.Detect(ctx, s.loginEnv(ctx).LookPath)
		}
	}
	for {
		item, ok := <-incoming
		if !ok {
			return nil
		}
		line, err := item.line, item.err
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			_ = conn.Write(errorResponse(nil, rpc.CodeParseError, err.Error(), nil))
			return err
		}
		var req rpc.Request
		if err := json.Unmarshal(line, &req); err != nil {
			if werr := conn.Write(errorResponse(nil, rpc.CodeParseError, "invalid JSON", nil)); werr != nil {
				return werr
			}
			continue
		}
		if req.JSONRPC != rpc.JSONRPCVersion || req.Method == "" {
			if werr := conn.Write(errorResponse(req.ID, rpc.CodeInvalidRequest, "invalid request", nil)); werr != nil {
				return werr
			}
			continue
		}
		if req.ID == nil {
			continue // notifications are not used yet
		}
		var resp rpc.Response
		switch {
		case req.Method == rpc.MethodHello:
			resp = hello(ctx, req, cfg)
			helloed = resp.Error == nil
		case !helloed:
			resp = errorResponse(req.ID, rpc.CodeHelloRequired, "first request must be hello", nil)
		default:
			id := req.ID
			resp = s.call(rpc.WithProgress(callCtx, func(stage string, done, total int64) {
				p, _ := json.Marshal(rpc.ProgressParams{ID: id, Stage: stage, Done: done, Total: total})
				_ = conn.Write(rpc.Request{JSONRPC: rpc.JSONRPCVersion, Method: rpc.MethodProgress, Params: p})
			}), req)
		}
		if err := conn.Write(resp); err != nil {
			return err
		}
	}
}

func hello(ctx context.Context, req rpc.Request, cfg Config) rpc.Response {
	var p rpc.HelloParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return errorResponse(req.ID, rpc.CodeInvalidParams, "invalid hello params", nil)
	}
	if !rpc.Compatible(p.Protocol, rpc.ProtocolVersion) {
		return errorResponse(req.ID, rpc.CodeUserError, "protocol mismatch", rpc.ProtocolMismatch(p.Protocol, rpc.ProtocolVersion))
	}
	ops, err := pendingOps(cfg.Home)
	if err != nil {
		ue := valise.New("journal_unreadable", "无法读取服务器上的交换日志目录",
			"检查 ~/.local/state/valise/journal/ 的权限").WithDetail(err.Error())
		return errorResponse(req.ID, rpc.CodeUserError, "journal unreadable", ue)
	}
	actions, err := (actionjournal.Store{Home: cfg.Home}).Pending()
	if err != nil {
		return errorResponse(req.ID, rpc.CodeUserError, "action journal unreadable", valise.New("action_journal_unreadable", "无法读取配置操作记录", "保留现场并检查 action-journal 目录"))
	}
	pendingActions := []action.PendingAction{}
	for _, j := range actions {
		pendingActions = append(pendingActions, action.PendingAction{OperationID: j.ID, InstanceID: j.Plan.InstanceID, Phase: j.Phase, Published: j.Published})
	}
	res := rpc.HelloResult{
		Capabilities:   []string{action.CapabilityTransactions, identity.Capability, identity.ListCapability, protection.Capability, protection.CapabilityV2, protection.BackupCapability, protection.BackupCapabilityV2, protection.BackupCapabilityV3, sysfix.Capability, vaultrpc.JourneyCapability, vaultrpc.CopyCheckCapability, vaultrpc.CopyVerifyCapability, vaultrpc.CloudCapability, vaultrpc.CloudFormCapability, vaultrpc.TargetRemoveCapability, identity.LostSourceCapability, corerevive.BodyDirsCapability, corerevive.ReviewCapability, coreinstall.NativeCapability},
		PendingActions: pendingActions,
		PorterVersion:  cfg.Version,
		Protocol:       rpc.ProtocolVersion,
		OS:             runtime.GOOS,
		Arch:           runtime.GOARCH,
		Home:           cfg.Home,
		Tools:          cfg.DetectTools(ctx),
		PendingOps:     ops,
	}
	b, err := json.Marshal(res)
	if err != nil {
		return errorResponse(req.ID, rpc.CodeInternalError, err.Error(), nil)
	}
	return rpc.Response{JSONRPC: rpc.JSONRPCVersion, ID: req.ID, Result: b}
}

// pendingOps lists unfinished swap journals.
func pendingOps(home string) ([]rpc.PendingOp, error) {
	js, err := porterrevive.Store{Home: home}.Pending()
	if err != nil {
		return nil, err
	}
	ops := []rpc.PendingOp{}
	for _, j := range js {
		ops = append(ops, rpc.PendingOp{OpID: j.Plan.OpID, InstanceID: j.Plan.InstanceID, Mode: j.Plan.Mode, State: j.State})
	}
	return ops, nil
}

func errorResponse(id json.RawMessage, code int, msg string, ue *valise.UserError) rpc.Response {
	if id == nil {
		id = json.RawMessage("null")
	}
	return rpc.Response{JSONRPC: rpc.JSONRPCVersion, ID: id, Error: &rpc.Error{Code: code, Message: msg, Data: ue}}
}
