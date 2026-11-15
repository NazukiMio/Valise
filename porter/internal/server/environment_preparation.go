package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"

	"valise/core/environment"
	"valise/core/identity"
	"valise/core/protection"
	"valise/core/revive"
	"valise/core/rpc"
	"valise/core/valise"
	porterrevive "valise/porter/internal/revive"
)

// These controllers own the recovery lock internally. Do not add them to
// installationMutations, which would acquire the same lock a second time.
func init() {
	methods[environment.MethodPreparationBegin] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		if err := s.require(rpc.TierAdmin, environment.MethodPreparationBegin); err != nil {
			return nil, err
		}
		var p revive.EnvironmentPreparationParams
		if strictPreparationDecode(raw, &p) != nil || !preparationReference(p.Agent, p.OperationID) || !preparationHash(p.PreviewDigest) {
			return nil, errInvalidParams
		}
		record, err := s.runner(ctx).BeginEnvironmentPreparation(ctx, p)
		if err != nil {
			return nil, err
		}
		manifest, _ := protection.Digest(record.Manifest)
		return environment.PreparationReceipt{Agent: p.Agent, OperationID: p.OperationID,
			Snapshot: identity.SnapshotRef{VaultID: record.Params.Input.VaultID, SnapshotID: record.Plan.SnapshotID},
			State:    record.State, DataOperation: record.Plan.OpID, BindingDigest: record.BindingDigest, ManifestDigest: manifest}, nil
	}
	methods[environment.MethodPreparationStatus] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := preparationParams(raw)
		if err != nil {
			return nil, err
		}
		return preparationStatus(s, ctx, p)
	}
	for _, method := range []string{environment.MethodPreparationFinish, environment.MethodPreparationCancel} {
		methods[method] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
			if err := s.require(rpc.TierAdmin, method); err != nil {
				return nil, err
			}
			p, err := preparationParams(raw)
			if err != nil {
				return nil, err
			}
			runner := s.runner(ctx)
			if method == environment.MethodPreparationFinish {
				_, err = runner.FinishEnvironmentPreparation(ctx, p.Agent, p.OperationID)
			} else {
				_, err = runner.CancelEnvironmentPreparation(ctx, p.Agent, p.OperationID)
			}
			if err != nil {
				return nil, err
			}
			return preparationStatus(s, ctx, p)
		}
	}
	methods[environment.MethodPreparationCleanupReview] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		p, err := preparationParams(raw)
		if err != nil {
			return nil, err
		}
		v, err := s.runner(ctx).ReviewEnvironmentPreparationCleanup(ctx, p.Agent, p.OperationID)
		if err != nil {
			return nil, err
		}
		out := environment.PreparationCleanupReview{PreparationParams: p, Snapshot: v.Snapshot, State: v.State, Digest: v.Digest}
		for _, item := range v.Items {
			kind, root := item.Kind, item.Root
			if item.DisplayKind != "" {
				kind, root = item.DisplayKind, ""
			}
			out.Items = append(out.Items, environment.PreparationCleanupItem{Kind: kind, Root: root, Present: item.Present})
		}
		return out, nil
	}
	methods[environment.MethodPreparationCleanup] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		if err := s.require(rpc.TierAdmin, environment.MethodPreparationCleanup); err != nil {
			return nil, err
		}
		var p environment.PreparationCleanupParams
		if strictPreparationDecode(raw, &p) != nil || !preparationReference(p.Agent, p.OperationID) || !preparationHash(p.Digest) {
			return nil, errInvalidParams
		}
		if _, err := s.runner(ctx).CleanupEnvironmentPreparation(ctx, p.Agent, p.OperationID, p.Digest); err != nil {
			return nil, err
		}
		return preparationStatus(s, ctx, p.PreparationParams)
	}
	for _, method := range []string{environment.MethodPreparationBegin, environment.MethodPreparationFinish, environment.MethodPreparationStatus,
		environment.MethodPreparationCancel, environment.MethodPreparationCleanupReview, environment.MethodPreparationCleanup} {
		handler := methods[method]
		limit := 3 * time.Minute
		if method == environment.MethodPreparationBegin || method == environment.MethodPreparationFinish {
			limit = 30 * time.Minute
		}
		methods[method] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
			ctx, cancel := context.WithTimeout(ctx, limit)
			defer cancel()
			return handler(s, ctx, raw)
		}
	}
}

func strictPreparationDecode(raw json.RawMessage, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return errInvalidParams
	}
	if d.Decode(new(any)) != io.EOF {
		return errInvalidParams
	}
	return nil
}

func preparationReference(agent identity.AgentRef, op string) bool {
	return protection.ValidateAgent(agent) == nil && protection.ValidID(op)
}

func preparationHash(hash string) bool {
	if len(hash) != 64 {
		return false
	}
	for _, r := range hash {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func preparationParams(raw json.RawMessage) (environment.PreparationParams, error) {
	var p environment.PreparationParams
	if strictPreparationDecode(raw, &p) != nil || !preparationReference(p.Agent, p.OperationID) {
		return p, errInvalidParams
	}
	return p, nil
}

func preparationStatus(s *session, ctx context.Context, p environment.PreparationParams) (*environment.PreparationStatus, error) {
	v, err := s.runner(ctx).ReviewEnvironmentPreparation(ctx, p.Agent, p.OperationID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, valise.New("environment_prepare_not_found", "服务器尚未返回这项准备的记录，结果仍待核实", "检查连接后重新读取原操作；不要重复发送准备或上传")
		}
		return nil, err
	}
	return projectPreparation(v), nil
}

func projectPreparation(v *porterrevive.PreparationReview) *environment.PreparationStatus {
	out := &environment.PreparationStatus{Agent: v.Agent, OperationID: v.OperationID, Snapshot: v.Snapshot,
		State: v.State, Mode: v.Mode, Phase: v.Phase, FailureCode: v.FailureCode, IssueCode: v.IssueCode,
		Materials: v.Materials, Probe: v.Probe, Check: v.Check, CanCancel: v.CanCancel, CanReviewCleanup: v.CanReviewCleanup, Retired: v.Retired}
	if v.Native != nil {
		out.OriginalObserved, out.OriginalRunning, out.OriginalStopped = true, v.Native.Running, v.Native.Stopped
	} else if v.OriginalManaged != nil {
		out.OriginalObserved, out.OriginalRunning, out.OriginalStopped = true, v.OriginalManaged.Running, v.OriginalManaged.Stopped
	}
	return out
}
