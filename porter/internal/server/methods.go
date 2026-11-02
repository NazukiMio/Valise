package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"valise/core/enroll"
	"valise/core/install"
	"valise/core/rpc"
	"valise/core/valise"
	"valise/core/vault"
	"valise/core/vaultrpc"
	porterenroll "valise/porter/internal/enroll"
	porterinstall "valise/porter/internal/install"
	"valise/porter/internal/seal"
	"valise/porter/internal/timer"
)

// session holds per-connection state.
type session struct {
	cfg Config
	env *porterenroll.Env
	// challenge is the pending owner nonce; owner is set once it was
	// returned (owner.go).
	challenge []byte
	owner     bool
}

// daily reports whether this connection came in on the daily key.
func (s *session) daily() bool { return s.cfg.Tier == rpc.TierDaily }

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
	enroll.MethodInstanceList: func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		recs, err := porterenroll.ListRecords(s.cfg.Home)
		res := enroll.InstanceListResult{Instances: []enroll.InstanceSummary{}}
		for _, r := range recs {
			sum := enroll.InstanceSummary{InstanceID: r.InstanceID, ProfileID: r.ProfileID,
				ProfileVersion: r.ProfileVersion, Roots: r.Roots, Superseded: r.Superseded,
				Service: []enroll.ServiceState{}, Entries: []enroll.EntryState{}}
			if p, err := porterenroll.ReadProfile(s.cfg.Home, r.InstanceID); err == nil {
				sum.ProfileStatus = p.Status
				if r.Superseded == nil {
					sum.Service, sum.Entries = porterenroll.Live(ctx, s.loginEnv(ctx), p)
				}
			}
			res.Instances = append(res.Instances, sum)
		}
		return res, err
	},
	enroll.MethodInstanceWrite: func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		var p enroll.InstanceWriteParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, errInvalidParams
		}
		path, err := porterenroll.WriteRecord(s.cfg.Home, p.Record, p.Profile)
		return enroll.InstanceWriteResult{Path: path}, err
	},
	enroll.MethodEntryPortSet: func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		var p enroll.EntryPortSetParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, errInvalidParams
		}
		ports, err := porterenroll.SetEntryPort(s.cfg.Home, p.InstanceID, p.Entry, p.Port)
		return enroll.EntryPortSetResult{EntryPorts: ports}, err
	},
}

func init() {
	paths := func(s *session) seal.Paths { return seal.Paths{Home: s.cfg.Home} }
	methods[vaultrpc.MethodRecipientAdd] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		var p vaultrpc.RecipientAddParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, errInvalidParams
		}
		if s.daily() {
			// The daily key may only register the first recipient: before
			// that no snapshot exists that a planted key could open.
			have, err := seal.ReadRecipients(paths(s))
			if err != nil {
				return nil, err
			}
			if len(have) > 0 {
				if err := s.require(rpc.TierOwner, vaultrpc.MethodRecipientAdd); err != nil {
					return nil, err
				}
			}
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
		var p vaultrpc.FilesParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, errInvalidParams
		}
		return seal.Files(paths(s), p.Leaf)
	}
	methods[vaultrpc.MethodServerNameSet] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		var p vaultrpc.ServerNameSetParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, errInvalidParams
		}
		return struct{}{}, seal.SetServerName(paths(s), p.Name)
	}
	methods[vaultrpc.MethodPeriodSet] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		var p vaultrpc.PeriodSetParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, errInvalidParams
		}
		return struct{}{}, seal.SetPeriodSetting(paths(s), p.InstanceID, p.Setting)
	}
	methods[vaultrpc.MethodLeafDelete] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		var p vaultrpc.LeafDeleteParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, errInvalidParams
		}
		st, err := seal.Status(paths(s))
		if err != nil {
			return nil, err
		}
		tier := rpc.TierAdmin
		if leafFoundElsewhere(st, p.Leaf) {
			tier = rpc.TierOwner
		}
		if err := s.require(tier, vaultrpc.MethodLeafDelete); err != nil {
			return nil, err
		}
		return seal.DeleteLeaf(paths(s), p.Leaf, vaultrpc.InitiatorClient)
	}
	methods[vaultrpc.MethodTimerSet] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		var p vaultrpc.TimerSetParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, errInvalidParams
		}
		return timer.Set(ctx, s.loginEnv(ctx), s.cfg.Home, p)
	}
	methods[vaultrpc.MethodTimerStatus] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		var p vaultrpc.TimerStatusParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, errInvalidParams
		}
		return timer.Status(ctx, s.loginEnv(ctx), s.cfg.Home, p.InstanceID)
	}
	methods[vaultrpc.MethodSealLog] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		var p vaultrpc.SealLogParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, errInvalidParams
		}
		return seal.SealLog(paths(s), p.InstanceID)
	}
	methods[vaultrpc.MethodTargetList] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		var p vaultrpc.TargetListParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, errInvalidParams
		}
		return seal.TargetList(ctx, s.loginEnv(ctx), paths(s), p.Target)
	}
	methods[vaultrpc.MethodTargetLeafDel] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		var p vaultrpc.TargetLeafDeleteParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, errInvalidParams
		}
		st, err := seal.Status(paths(s))
		if err != nil {
			return nil, err
		}
		tier := rpc.TierAdmin
		if leafOnServer(st, p.Leaf) {
			tier = rpc.TierOwner
		}
		if err := s.require(tier, vaultrpc.MethodTargetLeafDel); err != nil {
			return nil, err
		}
		return struct{}{}, seal.TargetLeafDelete(ctx, s.loginEnv(ctx), paths(s), p.Target, p.Leaf, vaultrpc.InitiatorClient)
	}
	methods[vaultrpc.MethodTargetSet] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		var p vaultrpc.TargetSetParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, errInvalidParams
		}
		return struct{}{}, seal.SetTarget(paths(s), p.Target)
	}
	methods[vaultrpc.MethodStationKey] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		var p vaultrpc.StationKeyParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, errInvalidParams
		}
		name, _ := seal.ReadServerName(paths(s))
		pub, err := seal.StationKeyFor(ctx, s.loginEnv(ctx), paths(s), p.Name, "valise-station@"+name)
		return vaultrpc.StationKeyResult{PublicKey: pub}, err
	}
	methods[vaultrpc.MethodStationCert] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		var p vaultrpc.StationCertParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, errInvalidParams
		}
		return struct{}{}, seal.SetStationCert(paths(s), p.Name, p.Cert)
	}
	methods[vaultrpc.MethodStationProbe] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		var p vaultrpc.StationKeyParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, errInvalidParams
		}
		out, err := seal.StationProbe(ctx, s.loginEnv(ctx), paths(s), p.Name)
		return vaultrpc.StationProbeResult{Output: out}, err
	}
	methods[vaultrpc.MethodSyncRecord] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		var p vaultrpc.SyncRecordParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, errInvalidParams
		}
		if p.Forget != "" {
			return struct{}{}, seal.ForgetSynced(paths(s), p.Target, p.Forget)
		}
		return struct{}{}, seal.RecordSynced(paths(s), p.Target, p.Snapshots)
	}
	methods[vaultrpc.MethodSync] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		var p vaultrpc.SyncParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, errInvalidParams
		}
		return seal.Sync(ctx, s.loginEnv(ctx), paths(s), p.Target, vaultrpc.InitiatorClient)
	}
}

func init() {
	methods[install.MethodCheck] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		var p install.CheckParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, errInvalidParams
		}
		return porterinstall.Check(ctx, s.loginEnv(ctx), p), nil
	}
	methods[install.MethodAudit] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		var p install.AuditParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, errInvalidParams
		}
		var steps []string
		for _, st := range p.Steps {
			steps = append(steps, fmt.Sprintf("%s %q exit %d", st.Kind, st.Command, st.Exit))
		}
		op := p.Op
		switch op {
		case "":
			op = install.AuditInstall
		case install.AuditInstall, install.AuditPortChange, install.AuditFullAccess:
		default:
			return nil, errInvalidParams
		}
		seal.Paths{Home: s.cfg.Home}.AppendAudit(seal.AuditEntry{Time: time.Now().UTC(), Op: op, Instance: p.Instance,
			Initiator: vaultrpc.InitiatorClient, Result: p.Result,
			Reason: auditReason(p, steps)})
		return struct{}{}, nil
	}
}

var errInvalidParams = &rpc.Error{Code: rpc.CodeInvalidParams, Message: "invalid params"}

func (s *session) call(ctx context.Context, req rpc.Request) rpc.Response {
	h, ok := methods[req.Method]
	if !ok {
		return errorResponse(req.ID, rpc.CodeMethodNotFound, "method not found: "+req.Method, nil)
	}
	if err := s.require(rpc.TierOf(req.Method), req.Method); err != nil {
		ue := err.(*valise.UserError)
		return errorResponse(req.ID, rpc.CodeUserError, ue.Code, ue)
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

func auditReason(p install.AuditParams, steps []string) string {
	r := p.PlanID
	if p.ProfileID != "" {
		r += fmt.Sprintf(" (%s v%d)", p.ProfileID, p.ProfileVersion)
	}
	if len(steps) > 0 {
		r += ": " + strings.Join(steps, "; ")
	}
	return r
}
