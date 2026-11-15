package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"valise/core/environment"
	"valise/core/identity"
	"valise/core/journey"
	"valise/core/protection"
	"valise/core/revive"
	"valise/core/vault"
)

const StageOldCleanup = "environment_old_cleanup"

type PreparedOldCleanup struct {
	View     environment.OldCleanupReview
	Expires  time.Time
	task     RestoreTask
	digest   string
	allowed  bool
	mu       sync.Mutex
	consumed bool
}

func (p *PreparedOldCleanup) Task() RestoreTask {
	t := p.task
	if t.TargetAgent != nil {
		agent := *t.TargetAgent
		t.TargetAgent = &agent
	}
	return t
}

func (s *Service) PrepareOldCleanup(ctx context.Context, task RestoreTask) (*PreparedOldCleanup, error) {
	if err := s.checkParallelTask(task); err != nil {
		return nil, err
	}
	var v environment.OldCleanupReview
	if err := s.call(ctx, task.Target, environment.MethodOldCleanupReview, parallelTaskParams(task), &v); err != nil {
		return nil, err
	}
	return prepareOldCleanupView(task, v)
}

// PrepareCurrentOldCleanup starts from the server's existing operation, so a
// second human client does not need another computer's local restore index.
func (s *Service) PrepareCurrentOldCleanup(ctx context.Context, host string, agent identity.AgentRef) (*PreparedOldCleanup, error) {
	if protection.ValidateAgent(agent) != nil {
		return nil, restoreError("record_corrupt", "请重新读取这个 Agent 的身份")
	}
	trust, err := s.restoreTrust(host)
	if err != nil {
		return nil, err
	}
	var v environment.OldCleanupReview
	if err := s.call(ctx, host, environment.MethodCurrentOldCleanup, environment.SettingsParams{Agent: agent}, &v); err != nil {
		return nil, err
	}
	if v.Agent != agent || !protection.ValidID(v.OperationID) || !vault.ValidID(v.Snapshot.VaultID) || !vault.ValidID(v.Snapshot.SnapshotID) || v.PolicyMode != environment.Direct {
		return nil, restoreError("source_conflict", "服务器的旧环境清理范围与当前 Agent 或直通设置不一致")
	}
	task := RestoreTask{Kind: RestoreKindEnvironment, TargetAgent: &agent, OpID: v.OperationID, Target: host, From: host, Instance: agent.InstanceID, Mode: revive.ModeRollback,
		EnvironmentMode: v.Mode, TrustDigest: trust, Snapshot: journey.SnapshotRef{VaultID: v.Snapshot.VaultID, SnapshotID: v.Snapshot.SnapshotID}, RuntimeRequested: true, State: "succeeded", Created: time.Now().UTC()}
	if raw, err := os.ReadFile(s.restoreTaskPath(task.OpID)); err == nil {
		var previous RestoreTask
		if json.Unmarshal(raw, &previous) != nil || s.checkParallelTask(previous) != nil {
			return nil, restoreError("record_corrupt", "本机原恢复记录无法核实，请检查任务记录与服务器设置")
		}
		if previous.Target != host || previous.TargetAgent == nil || *previous.TargetAgent != agent || previous.Snapshot != task.Snapshot || previous.EnvironmentMode != task.EnvironmentMode {
			return nil, restoreError("source_conflict", "本机已有同编号记录，但身份或备份与服务器不一致")
		}
		task.SnapshotTime, task.SnapshotTier, task.Created = previous.SnapshotTime, previous.SnapshotTier, previous.Created
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := s.checkParallelTask(task); err != nil {
		return nil, err
	}
	task.OldCleanupState = "retained"
	if v.State == "pending" || v.State == "cleaned" {
		task.OldCleanupState = v.State
	}
	return prepareOldCleanupView(task, v)
}

func prepareOldCleanupView(task RestoreTask, v environment.OldCleanupReview) (*PreparedOldCleanup, error) {
	validRef := func(r environment.Ref) bool {
		return (r.Slot == environment.A || r.Slot == environment.B) && r.Revision > 0
	}
	if v.PreparationParams != parallelTaskParams(task) || !parallelSnapshotMatches(task, v.Snapshot) || (v.Mode != environment.Compact && (v.Mode != environment.Parallel || v.PolicyMode != environment.Direct)) || task.EnvironmentMode != v.Mode || !validRef(v.Target) || !validRef(v.Current) || v.Target.Slot == v.Current.Slot || len(v.Digest) != 64 || (v.State != "ready" && v.State != "pending" && v.State != "cleaned") {
		return nil, restoreError("source_conflict", "旧环境清理范围与原恢复记录不一致")
	}
	seen, programs, temporary, data := map[string]bool{}, 0, 0, 0
	for _, item := range v.Items {
		key := item.Kind + ":" + item.Root
		if seen[key] || item.Files < 0 || item.Bytes < 0 {
			return nil, restoreError("source_conflict", "服务器返回了不完整的清理范围")
		}
		seen[key] = true
		switch item.Kind {
		case "program":
			if item.Root != "" {
				return nil, restoreError("source_conflict", "独立程序清理范围不一致")
			}
			programs++
		case "preparation":
			if item.Root != "" {
				return nil, restoreError("source_conflict", "临时材料清理范围不一致")
			}
			temporary++
		case "data":
			if item.Root == "" {
				return nil, restoreError("source_conflict", "数据清理范围缺少固定根")
			}
			data++
		default:
			return nil, restoreError("source_conflict", "服务器返回了未知清理种类")
		}
	}
	if programs != 1 || temporary != 1 || data < 1 {
		return nil, restoreError("source_conflict", "服务器返回了不完整的旧环境清理范围")
	}
	return &PreparedOldCleanup{View: v, Expires: time.Now().Add(10 * time.Minute), task: task, digest: v.Digest, allowed: v.State != "cleaned"}, nil
}

func (s *Service) RunOldCleanup(ctx context.Context, p *PreparedOldCleanup, changed func(RestoreTask)) (RestoreTask, *environment.SwitchStatus, error) {
	if p == nil {
		return RestoreTask{}, nil, restoreError("plan_stale", "请先查看旧环境的清理范围")
	}
	task := p.task
	p.mu.Lock()
	if p.consumed || time.Now().After(p.Expires) || ctx.Err() != nil {
		p.mu.Unlock()
		return task, nil, restoreError("confirmation_expired", "清理审阅已使用、停止或过期，请重新读取原操作")
	}
	if !p.allowed {
		p.mu.Unlock()
		return task, nil, restoreError("confirmation_required", "这份审阅没有可执行的清理范围")
	}
	p.consumed = true
	p.mu.Unlock()
	if err := s.checkParallelTask(task); err != nil {
		return task, nil, err
	}
	if err := os.MkdirAll(filepath.Dir(s.restoreTaskPath(task.OpID)), 0700); err != nil {
		return task, nil, err
	}
	unlock, err := protectionStoreLock(s.restoreTaskPath(task.OpID) + ".lock")
	if err != nil {
		return task, nil, err
	}
	defer unlock()
	publish := func() {
		if changed != nil {
			changed(task)
		}
	}
	// Cleanup status is independent from the accepted restore. The local
	// index cannot turn a cleanup request or failure into a failed restoration.
	task.State, task.Stage, task.OldCleanupState, task.RuntimeRequested = "succeeded", "environment_old_cleanup_login", "unverified", true
	if err := s.saveRestore(task); err != nil {
		return task, nil, err
	}
	publish()
	report(ctx, task.Stage, 0, 0)
	err = s.withPreparationManagement(ctx, task.Target, func(o *ownerPorter) error {
		if ctx.Err() != nil || time.Now().After(p.Expires) {
			return restoreError("confirmation_expired", "管理登录期间清理确认已停止或过期，没有发送清理")
		}
		if err := s.checkParallelTask(task); err != nil {
			return err
		}
		task.Stage = StageOldCleanup
		if err := s.saveRestore(task); err != nil {
			return err
		}
		publish()
		report(ctx, task.Stage, 0, 0)
		return s.callPreparation(ctx, o, environment.MethodOldCleanup, environment.ReviewedOperationParams{PreparationParams: parallelTaskParams(task), Digest: p.digest}, new(environment.SwitchStatus))
	})
	task.Stage = ""
	query, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer cancel()
	observed, status, queryErr := s.InspectParallelSwitch(query, task)
	if queryErr == nil {
		task = observed
	} else if saveErr := s.saveRestore(task); saveErr != nil {
		return task, nil, saveErr
	}
	publish()
	if err != nil {
		return task, status, err
	}
	return task, status, queryErr
}
