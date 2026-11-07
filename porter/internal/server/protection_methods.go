package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"valise/core/enroll"
	"valise/core/identity"
	"valise/core/protection"
	"valise/porter/internal/backuprun"
	porterenroll "valise/porter/internal/enroll"
	porteridentity "valise/porter/internal/identity"
	porterprotection "valise/porter/internal/protection"
)

// validateAgent checks the current generation and dossier facts. A stable ID
// cannot authorize work against roots replaced outside the acceptance flow.
func (s *session) validateAgent(ctx context.Context, ref identity.AgentRef) error {
	return porterenroll.ValidateIdentity(ctx, s.cfg.Home, ref)
}

func (s *session) protectionStore(ctx context.Context) porterprotection.Store {
	st := porterprotection.Store{Home: s.cfg.Home, Env: s.loginEnv(ctx), ValidateAgent: s.validateAgent}
	// Only a porter running from the formal install path guards against a
	// downgrade there; an explicit build run from elsewhere is not the
	// deployed porter and is not claimed to be protected.
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			if formal, err := filepath.EvalSymlinks(porterprotection.DeployedPorter(s.cfg.Home)); err == nil && formal == real {
				st.ValidateDeployment = func(ctx context.Context) error {
					return porterprotection.CheckDeployment(ctx, s.loginEnv(ctx), s.cfg.Home)
				}
			}
		}
	}
	return st
}

func init() {
	methods[protection.MethodSnapshotBinding] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		var p protection.SnapshotBindingParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, errInvalidParams
		}
		return (backuprun.Store{Home: s.cfg.Home}).SnapshotBinding(ctx, p)
	}
	methods[protection.MethodBackup] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		var p protection.BackupParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, errInvalidParams
		}
		return (backuprun.Store{Home: s.cfg.Home, Env: s.loginEnv(ctx), ValidateAgent: s.validateAgent}).Run(ctx, p)
	}
	methods[protection.MethodRunStatus] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		var p protection.BackupStatusParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, errInvalidParams
		}
		return (backuprun.Store{Home: s.cfg.Home, Env: s.loginEnv(ctx), ValidateAgent: s.validateAgent}).Status(ctx, p)
	}
	methods[identity.MethodGet] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		var p identity.GetParams
		if err := json.Unmarshal(raw, &p); err != nil || enroll.ValidInstanceID(p.InstanceID) != nil {
			return nil, errInvalidParams
		}
		return porteridentity.Read(s.cfg.Home, p.InstanceID)
	}
	methods[protection.MethodGet] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		var p protection.GetParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, errInvalidParams
		}
		return s.protectionStore(ctx).Get(ctx, p)
	}
	methods[protection.MethodPrepare] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		var p protection.PrepareParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, errInvalidParams
		}
		return s.protectionStore(ctx).Prepare(ctx, p)
	}
	methods[protection.MethodApply] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		var p protection.ApplyParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, errInvalidParams
		}
		return s.protectionStore(ctx).Apply(ctx, p)
	}
	methods[protection.MethodReconcile] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		var p protection.ReconcileParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, errInvalidParams
		}
		return s.protectionStore(ctx).Reconcile(ctx, p)
	}
	methods[protection.MethodStatus] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		var p protection.StatusParams
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, errInvalidParams
		}
		return s.protectionStore(ctx).Status(ctx, p)
	}
}
