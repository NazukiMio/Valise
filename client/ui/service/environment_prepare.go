package service

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"

	"valise/client/ssh"
	"valise/core/environment"
	"valise/core/identity"
	"valise/core/protection"
	"valise/core/revive"
	"valise/core/valise"
)

const RestoreKindEnvironment = "environment"

// This object owns decrypted sources, has no JSON execution representation,
// and is consumed once. Persisted RestoreTask values never restore its consent.
type PreparedParallelRestore struct {
	View                revive.EnvironmentPlan
	Materials           *TestMaterialReview
	RequiresCredentials bool
	plan                *RevivePlan
	params              revive.EnvironmentPlanParams
	request             RestoreRequest
	trust               string
	Expires             time.Time
	mu                  sync.Mutex
	consumed            bool
	reuseRequired       bool
}

func (p *PreparedParallelRestore) Close() {
	if p != nil && p.plan != nil {
		p.plan.Close()
	}
}

// ParallelPrepareChoice is supplied by the native human adapter for this
// displayed plan. Reuse covers only the listed inactive resources; it grants
// no activation or general deletion permission.
type ParallelPrepareChoice struct {
	Reuse         bool
	Credentials   bool
	AllowUntested bool
	Baseline      bool
}

func (s *Service) RunParallelPreparation(ctx context.Context, p *PreparedParallelRestore, choice ParallelPrepareChoice, changed func(RestoreTask)) (RestoreTask, error) {
	var task RestoreTask
	if p == nil || p.plan == nil {
		return task, restoreError("plan_stale", "请先查看这个时间点的并行恢复条件")
	}
	p.mu.Lock()
	if err := ctx.Err(); err != nil {
		p.mu.Unlock()
		return task, err
	}
	if p.consumed || time.Now().After(p.Expires) {
		p.mu.Unlock()
		return task, restoreError("confirmation_expired", "准备条件已使用或过期，请重新读取；已有操作只能查询")
	}
	if p.RequiresCredentials && !choice.Credentials || p.View.ProfileUntested && !choice.AllowUntested || p.Materials != nil && !choice.Baseline {
		p.mu.Unlock()
		return task, valise.New("confirmation_required", "还没有确认本次准备的凭据、未核实能力或底座影响", "查看当前计划的各项影响后再准备")
	}
	if p.reuseRequired && !choice.Reuse {
		p.mu.Unlock()
		return task, valise.New("confirmation_required", "还没有确认本次复用另一环境的影响", "查看将覆盖的旧材料，再选择复用并准备")
	}
	p.consumed = true
	p.mu.Unlock()
	defer p.Close()
	trust, err := s.restoreTrust(p.plan.Target)
	if err != nil || trust != p.trust {
		return task, restoreError("plan_stale", "服务器连接或信任身份已经变化，没有开始准备")
	}
	op, err := identity.NewID()
	if err != nil {
		return task, err
	}
	task = RestoreTask{Kind: RestoreKindEnvironment, EnvironmentMode: p.View.Mode, TargetAgent: &p.params.Agent,
		OpID: op, Target: p.plan.Target, From: p.plan.From, Mode: revive.ModeRollback,
		Snapshot: p.request.Snapshot, SnapshotTime: p.params.Input.Header.CreatedAt, SnapshotTier: p.params.Input.Header.Tier,
		Instance: p.params.Agent.InstanceID, State: "running", Stage: "environment_capture", TrustDigest: trust, Created: time.Now().UTC()}
	if err := os.MkdirAll(filepath.Dir(s.restoreTaskPath(task.OpID)), 0700); err != nil {
		return task, err
	}
	unlock, err := protectionStoreLock(s.restoreTaskPath(task.OpID) + ".lock")
	if err != nil {
		return task, err
	}
	defer unlock()
	if _, err := os.Stat(s.restoreTaskPath(task.OpID)); !os.IsNotExist(err) {
		return task, restoreError("request_conflict", "这项准备已有任务记录，请查询原操作")
	}
	if err := s.saveRestore(task); err != nil {
		return task, err
	}
	publish := func() {
		if changed != nil {
			changed(task)
		}
	}
	publish()
	report(ctx, task.Stage, 0, 0)
	err = s.withPreparationManagement(ctx, task.Target, func(operation *ownerPorter) error {
		// Password prompts and a new management connection may outlast the
		// review. Recheck before the first server-side write, not just on click.
		if time.Now().After(p.Expires) {
			return restoreError("confirmation_expired", "管理登录期间准备确认已过期，没有发送准备请求")
		}
		if trust, err := s.restoreTrust(task.Target); err != nil || trust != task.TrustDigest {
			return restoreError("plan_stale", "管理登录期间服务器连接或信任身份已变化，没有发送准备请求")
		}
		var receipt environment.PreparationReceipt
		params := revive.EnvironmentPreparationParams{EnvironmentPlanParams: p.params, OperationID: task.OpID, PreviewDigest: p.View.Digest, AllowUntested: choice.AllowUntested}
		if err := s.callPreparation(ctx, operation, environment.MethodPreparationBegin, params, &receipt); err != nil {
			return err
		}
		manifest, _ := protection.Digest(parallelManifest(p.plan))
		if receipt.Agent != p.params.Agent || receipt.OperationID != task.OpID || !parallelSnapshotMatches(task, receipt.Snapshot) || receipt.State != "data_ready" ||
			!revive.ValidOpID(receipt.DataOperation) || len(receipt.BindingDigest) != 64 || receipt.ManifestDigest != manifest {
			return restoreError("source_conflict", "准备收据与已确认的时间点或文件范围不一致，没有上传")
		}
		p.plan.Plan.OpID, p.plan.target = receipt.DataOperation, operation
		task.Stage = StageStage
		if err := s.saveRestore(task); err != nil {
			return err
		}
		publish()
		report(ctx, StageStage, 0, p.plan.Bytes)
		if err := s.streamParallelStage(ctx, p.plan); err != nil {
			return err
		}
		task.Stage = "environment_build"
		if err := s.saveRestore(task); err != nil {
			return err
		}
		publish()
		report(ctx, task.Stage, 0, 0)
		return s.callPreparation(ctx, operation, environment.MethodPreparationFinish, parallelTaskParams(task), new(environment.PreparationStatus))
	})
	// A lost reply is not a retry. Only the original server operation settles
	// what happened; even a failed begin retains this local operation identity.
	task.State, task.Stage = "reconciling", ""
	query, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer cancel()
	observed, _, queryErr := s.InspectParallelPreparation(query, task)
	if queryErr == nil {
		task = observed
	} else if saveErr := s.saveRestore(task); saveErr != nil {
		return task, saveErr
	}
	publish()
	if err != nil {
		return task, err
	}
	return task, queryErr
}

func (s *Service) withPreparationManagement(ctx context.Context, host string, run func(*ownerPorter) error) error {
	t, done, err := s.adminTransport(ctx, host)
	if err != nil {
		return err
	}
	defer done()
	found, err := s.porterOn(ctx, t, false)
	if err != nil {
		return err
	}
	return run(&ownerPorter{host: host, t: t, path: found.Path})
}

func (s *Service) callPreparation(ctx context.Context, operation *ownerPorter, method string, params, result any) error {
	c, hello, err := ssh.DialEnv(ctx, operation.t, operation.path, s.Version, nil)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := n2Compatibility(hello, method, params); err != nil {
		return err
	}
	return c.Call(ctx, method, params, result)
}
