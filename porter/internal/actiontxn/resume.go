package actiontxn

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"valise/core/action"
	"valise/porter/internal/actionjournal"
	"valise/porter/internal/seal"
)

func (r Runner) Resume(ctx context.Context, p action.ResumeParams) (*action.RunResult, error) {
	if p.Choice != action.ResumeContinue && p.Choice != action.ResumeRestore {
		return nil, reject("未选择有效恢复方式")
	}
	lock, unlock, err := (seal.Paths{Home: r.Env.Home}).ActionLock(p.OperationID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	s := actionjournal.Store{Home: r.Env.Home}
	j, err := s.Load(p.OperationID)
	if err != nil {
		return nil, err
	}
	if err = checkPlan(&j.Plan); err != nil {
		return nil, err
	}
	if j.Done {
		exit := 0
		if j.Phase != "complete" {
			exit = -1
		}
		return &action.RunResult{Exit: exit, Operation: j}, nil
	}
	e, err := r.environment(j)
	if err != nil {
		return nil, err
	}
	if err = r.idle(j.Stage, j.Previous); err != nil {
		return nil, err
	}
	if j.Phase == "move-live-intent" || j.Phase == "publish-intent" || j.Published {
		if err = s.Reconcile(j); err != nil {
			return nil, err
		}
	} else {
		id, identityErr := actionjournal.DirectoryIdentity(j.Plan.Live)
		if identityErr != nil || id != j.Plan.LiveIdentity {
			return nil, reject("活动目录身份已变化，不能恢复")
		}
	}
	if j.Published {
		return r.finish(ctx, e, lock, s, j)
	}
	if p.Choice == action.ResumeRestore {
		return r.failed(ctx, s, j, "用户放弃尚未发布的暂存配置", nil)
	}
	// Continuing never runs a configuration step. Only a fully verified stage qualifies.
	if !j.StageVerified || j.StageTreeDigest == "" || len(j.Steps) != len(j.Plan.Definition.Steps) {
		return nil, reject("步骤结果不明，不能继续发布；请选择恢复原服务状态")
	}
	for _, step := range j.Steps {
		if !step.Completed || step.Exit == nil || *step.Exit != 0 {
			return nil, reject("配置步骤尚未全部成功，不能继续发布")
		}
	}
	for _, sv := range j.Plan.Services {
		state, identity, err := serviceState(ctx, e, sv.Unit)
		if err != nil || identity != sv.Identity || state != "inactive" {
			return nil, reject("服务现场已变化，不能继续发布")
		}
	}
	if err = r.idle(j.Plan.Live, j.Stage); err != nil {
		return nil, err
	}
	matched, verifyErr := verify(ctx, stageEnvironment(e, j), lock, j.Stage, &j.Plan.Approval)
	if !matched || verifyErr != nil {
		return r.failed(ctx, s, j, "重新检查暂存配置未通过", verifyErr)
	}
	if err = s.Publish(j); err != nil {
		return r.failed(ctx, s, j, "继续发布未完成", err)
	}
	return r.finish(ctx, e, lock, s, j)
}

// Cleanup is invoked only after an explicit user request and confirmation.
// It checks exact directory identities and never removes the active root.
func (r Runner) Cleanup(ctx context.Context, p action.CleanupParams) ([]string, error) {
	_, unlock, err := (seal.Paths{Home: r.Env.Home}).ActionLock(p.OperationID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	s := actionjournal.Store{Home: r.Env.Home}
	j, err := s.Load(p.OperationID)
	if err != nil {
		return nil, err
	}
	if !j.Done || j.Services != "restored" {
		return nil, reject("配置操作仍未恢复原服务状态，不能清理")
	}
	if j.Cleaned {
		return nil, nil
	}
	if err = checkPlan(&j.Plan); err != nil {
		return nil, err
	}
	if err = actionjournal.ValidatePaths(j); err != nil {
		return nil, err
	}
	liveID, liveErr := actionjournal.DirectoryIdentity(j.Plan.Live)
	expected := j.Plan.LiveIdentity
	if j.Published {
		expected = j.StageIdentity
	}
	if liveErr != nil || liveID != expected {
		return nil, reject("活动目录身份发生变化，不能清理")
	}
	var paths []string
	for _, item := range []struct {
		path string
		id   action.Identity
	}{{j.Stage, j.StageIdentity}, {j.Previous, j.Plan.LiveIdentity}} {
		id, err := actionjournal.DirectoryIdentity(item.path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || id != item.id || id.Inode == 0 {
			return nil, reject("待清理目录身份不符，保留现场")
		}
		paths = append(paths, item.path)
	}
	if err = r.checkCleanupRoots(paths); err != nil {
		return nil, err
	}
	if !p.Confirm {
		return paths, nil
	}
	if err = r.idle(paths...); err != nil {
		return nil, err
	}
	j.CleanupStarted = true
	if err = s.Save(j); err != nil {
		return nil, err
	}
	for _, path := range paths {
		if err = os.RemoveAll(path); err != nil {
			return nil, err
		}
	}
	// Remove only the now-empty operation wrapper, preserving unexpected files.
	if err = os.Remove(filepath.Dir(j.Stage)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	j.Cleaned = true
	if err = s.Save(j); err != nil {
		return nil, err
	}
	return paths, nil
}
