package service

import (
	"context"
	"path/filepath"
	"sync"
	"time"

	"valise/core/environment"
	"valise/core/valise"
)

const StageEnvironmentConfirm = "environment_confirm"

// PreparedParallelSwitch is a single human process's short-lived review. No
// JSON task record can reconstruct its private execution fields or consent.
type PreparedParallelSwitch struct {
	View     environment.SwitchReview
	Expires  time.Time
	task     RestoreTask
	action   string
	digest   string
	allowed  bool
	untested bool
	mu       sync.Mutex
	consumed bool
}

func (s *Service) PrepareParallelSwitch(ctx context.Context, task RestoreTask, action string) (*PreparedParallelSwitch, error) {
	if err := s.checkParallelTask(task); err != nil {
		return nil, err
	}
	if action != "switch" && action != "return" && action != "confirm" {
		return nil, restoreError("plan_stale", "没有选择可用的切换、切回或确认操作")
	}
	var view environment.SwitchReview
	if err := s.call(ctx, task.Target, environment.MethodSwitchReview, parallelTaskParams(task), &view); err != nil {
		return nil, err
	}
	if err := validateSwitchStatus(task, &view.Status); err != nil {
		return nil, err
	}
	if view.Status.Historical && (view.CanSwitch || view.CanReturn || view.CanConfirm || view.Digest != "" || view.ConfirmationDigest != "") {
		return nil, restoreError("source_conflict", "历史恢复记录不能提供当前操作")
	}
	if view.CanSwitch && (!view.ProgramsChecked || !view.OriginalProgramChecked || !view.CandidateProgramChecked || !view.StageChecked || !view.SourceChecked ||
		view.Status.Phase != environment.Prepared || view.Status.State != "ready" || !view.Status.CurrentObserved || !view.Status.CurrentRunning || view.Status.Current == nil || *view.Status.Current != view.Status.Original) {
		return nil, restoreError("source_conflict", "切换审阅缺少已核实的程序、暂存或原进程事实")
	}
	if view.CanConfirm && (len(view.ConfirmationDigest) != 64 || !view.CandidateProgramChecked || view.Status.Phase != environment.AwaitingHuman || !view.Status.CurrentObserved || !view.Status.CurrentRunning || view.Status.Current == nil || *view.Status.Current != view.Status.Candidate) {
		return nil, restoreError("source_conflict", "确认审阅缺少当前候选程序和实际运行身份")
	}
	if view.CanReturn && !view.OriginalProgramChecked || (view.CanReturn || view.CanSwitch || view.CanConfirm) && len(view.Digest) != 64 || view.Files < 0 || view.Bytes < 0 {
		return nil, restoreError("source_conflict", "服务器返回了不完整的切换审阅")
	}
	allowed := action == "switch" && view.CanSwitch || action == "return" && view.CanReturn || action == "confirm" && view.CanConfirm
	digest := view.Digest
	if action == "confirm" {
		digest = view.ConfirmationDigest
	}
	return &PreparedParallelSwitch{View: view, Expires: time.Now().Add(10 * time.Minute), task: task, action: action, digest: digest, allowed: allowed, untested: action == "switch" && view.ProfileUntested}, nil
}

func (s *Service) RunParallelSwitch(ctx context.Context, p *PreparedParallelSwitch, allowUntested bool, changed func(RestoreTask)) (RestoreTask, *environment.SwitchStatus, error) {
	var empty RestoreTask
	if p == nil {
		return empty, nil, restoreError("plan_stale", "请先复核本次操作的条件")
	}
	task := p.task
	p.mu.Lock()
	if p.consumed || time.Now().After(p.Expires) || ctx.Err() != nil {
		p.mu.Unlock()
		return task, nil, restoreError("confirmation_expired", "本次审阅已使用、停止或过期，请重新核实原操作")
	}
	if !p.allowed || p.untested && !allowUntested {
		p.mu.Unlock()
		return task, nil, valise.New("confirmation_required", "本次条件或未核实能力尚未确认，没有发送操作", "查看当前审阅的检查项和影响后再操作")
	}
	p.consumed = true
	p.mu.Unlock()
	if err := s.checkParallelTask(task); err != nil {
		return task, nil, err
	}
	unlock, err := protectionStoreLock(filepath.Clean(s.restoreTaskPath(task.OpID)) + ".lock")
	if err != nil {
		return task, nil, err
	}
	defer unlock()
	publish := func() {
		if changed != nil {
			changed(task)
		}
	}
	task.State, task.Stage = "waiting", "environment_switch_login"
	if err := s.saveRestore(task); err != nil {
		return task, nil, err
	}
	publish()
	report(ctx, task.Stage, 0, 0)
	err = s.withPreparationManagement(ctx, task.Target, func(operation *ownerPorter) error {
		if ctx.Err() != nil || time.Now().After(p.Expires) {
			return restoreError("confirmation_expired", "管理登录期间操作确认已停止或过期，没有发送操作")
		}
		if err := s.checkParallelTask(task); err != nil {
			return err
		}
		task.State, task.Stage, task.RuntimeRequested = "running", StageSwap, true
		if p.action == "confirm" {
			task.Stage = StageEnvironmentConfirm
		}
		if err := s.saveRestore(task); err != nil {
			return err
		}
		publish()
		report(ctx, task.Stage, 0, 0)
		// From submission onwards a normal close waits. A dropped connection
		// later resolves only through the original parent record.
		run, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Minute)
		defer cancel()
		if p.action == "switch" {
			return s.callPreparation(run, operation, environment.MethodSwitch, environment.SwitchParams{PreparationParams: parallelTaskParams(task), Digest: p.digest, AllowUntested: allowUntested}, new(environment.SwitchStatus))
		}
		method := environment.MethodReturn
		if p.action == "confirm" {
			method = environment.MethodConfirm
		}
		return s.callPreparation(run, operation, method, environment.ReviewedOperationParams{PreparationParams: parallelTaskParams(task), Digest: p.digest}, new(environment.SwitchStatus))
	})
	task.State, task.Stage = "reconciling", ""
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

func validateSwitchStatus(t RestoreTask, v *environment.SwitchStatus) error {
	validRef := func(ref environment.Ref) bool {
		return (ref.Slot == environment.A || ref.Slot == environment.B) && ref.Revision != 0
	}
	if v == nil || t.TargetAgent == nil || v.Agent != *t.TargetAgent || v.OperationID != t.OpID || !parallelSnapshotMatches(t, v.Snapshot) || v.Mode != t.EnvironmentMode ||
		!validRef(v.Original) || !validRef(v.Candidate) || v.Original.Slot == v.Candidate.Slot || v.Current != nil && (*v.Current != v.Original && *v.Current != v.Candidate) {
		return restoreError("source_conflict", "服务器的切换记录与本机原操作或时间点不一致")
	}
	if v.Historical && (!environment.OperationFinished(v.Phase) || v.Current != nil || v.CurrentObserved || v.CurrentRunning || v.CurrentStopped || v.OriginalObserved || v.OriginalRunning || v.OriginalStopped || v.GatewayObserved || v.PortOwned || v.HTTPResponded || v.GatewayPassed || v.ConversationKnown || v.CanReviewOldCleanup) {
		return restoreError("source_conflict", "历史恢复记录混入了当前环境状态")
	}
	return nil
}

func (s *Service) InspectParallelSwitch(ctx context.Context, task RestoreTask) (RestoreTask, *environment.SwitchStatus, error) {
	if err := s.checkParallelTask(task); err != nil {
		return task, nil, err
	}
	var view environment.SwitchStatus
	if err := s.call(ctx, task.Target, environment.MethodSwitchStatus, parallelTaskParams(task), &view); err != nil {
		return task, nil, err
	}
	if err := validateSwitchStatus(task, &view); err != nil {
		return task, nil, err
	}
	task.State, task.Stage, task.SwapState = "needs_recovery", "", string(view.Phase)
	task.OldCleanupState = view.CleanupState
	if view.Historical {
		switch view.Phase {
		case environment.Accepted:
			if view.HistoricalConfirmed {
				task.State = "succeeded"
			}
		case environment.RolledBack:
			task.State = "failed"
		case environment.Cancelled:
			task.State = "cancelled"
		}
		return task, &view, s.saveRestore(task)
	}
	if view.CurrentObserved && view.Current != nil {
		switch {
		case view.Phase == environment.Prepared && *view.Current == view.Original && view.CurrentRunning:
			task.State = "prepared"
		case view.Phase == environment.AwaitingHuman && *view.Current == view.Candidate && view.CurrentRunning:
			task.State = "awaiting_human"
		case (view.Phase == environment.Accepted || view.Phase == environment.Cleaning) && *view.Current == view.Candidate && view.ConversationKnown && (view.CurrentRunning || view.CurrentStopped):
			task.State = "succeeded"
		case view.Phase == environment.RolledBack && *view.Current == view.Original && view.CurrentRunning:
			task.State = "failed"
		}
	}
	return task, &view, s.saveRestore(task)
}
