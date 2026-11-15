package actiontxn

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"valise/core/action"
	"valise/core/profile"
	"valise/porter/internal/actionjournal"
	porterenroll "valise/porter/internal/enroll"
	porterrevive "valise/porter/internal/revive"
	"valise/porter/internal/seal"
)

var errChildAlive = errors.New("configuration subprocess remains alive")
var errRepairRemaining = errors.New("approval policy does not match repair target")

type Runner struct {
	Env *porterenroll.Env
	// Idle is injected only by local tests; production requires Linux /proc.
	Idle func([]string) bool
}

func (r Runner) idle(roots ...string) error {
	if r.Idle != nil {
		if r.Idle(roots) {
			return nil
		}
		return reject("目录仍被其他进程使用")
	}
	if runtime.GOOS != "linux" {
		return reject("此系统无法核实配置进程，配置事务仅在已核实的 Linux 靶机开放")
	}
	if _, err := os.Stat("/proc/self/fd"); err != nil {
		return reject("无法检查活动进程")
	}
	if porterrevive.RootsBusy(roots) {
		return reject("目录仍被其他进程使用；不会结束用户进程")
	}
	return nil
}
func (r Runner) Run(ctx context.Context, p action.RunParams) (*action.RunResult, error) {
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return nil, err
	}
	id := "act_" + hex.EncodeToString(entropy[:])
	lock, unlock, err := (seal.Paths{Home: r.Env.Home}).ActionLock(id)
	if err != nil {
		return nil, err
	}
	defer unlock()

	s := actionjournal.Store{Home: r.Env.Home}
	pending, err := (porterrevive.Store{Home: r.Env.Home}).Pending()
	if err != nil {
		return nil, err
	}
	if len(pending) != 0 {
		return nil, reject("有未完成的恢复操作，先处理后再修改配置")
	}
	plan, err := Plan(ctx, r.Env, p.InstanceID, p.ActionID)
	if err != nil {
		return nil, err
	}
	if p.PlanDigest == "" || p.PlanDigest != plan.Digest {
		return nil, reject("执行计划在确认后发生变化，请重新确认")
	}
	if err = checkPlan(plan); err != nil {
		return nil, err
	}
	if len(plan.Links) > 0 && p.LinkHandling < action.LinkHandlingVersion {
		return nil, reject("状态目录里有需要特别处理的链接，这个客户端版本不能显示处理方式；请升级 Valise 后重新确认")
	}
	var disk syscall.Statfs_t
	if err = syscall.Statfs(filepath.Dir(plan.Live), &disk); err != nil {
		return nil, err
	}
	if uint64(disk.Bavail)*uint64(disk.Bsize) < uint64(plan.Bytes+plan.LinkBytes)+16<<20 {
		return nil, reject("可用空间不足以复制完整状态目录")
	}
	now := time.Now().UTC()
	private := filepath.Join(filepath.Dir(plan.Live), ".valise-action-"+id)
	j := &action.Operation{Format: action.OperationFormat, ID: id, Plan: *plan, BackupID: p.BackupID, Stage: filepath.Join(private, "stage"), Previous: filepath.Join(private, "prev"), Phase: "planned", Approval: "unknown", Services: "pending", Created: now, Stopped: map[string]bool{}, Restored: map[string]bool{}}
	if err = s.Save(j); err != nil {
		return nil, err
	}
	// Every created directory is preceded by the durable operation intent.
	if err = os.Mkdir(private, 0700); err != nil {
		return r.failed(ctx, s, j, "暂存目录无法创建", err)
	}
	e, err := r.environment(j)
	if err != nil {
		return r.failed(ctx, s, j, "实例选择环境发生变化", err)
	}
	if err = stopServices(ctx, e, s, j); err != nil {
		return r.failed(ctx, s, j, "停止原服务失败", err)
	}
	if err = r.idle(plan.Live); err != nil {
		return r.failed(ctx, s, j, "活动目录仍被使用", err)
	}
	// Read the state directory again now that the service is stopped and
	// freeze the readings into the record (INSTALL.md 5.2.1): the program
	// locations must be those confirmed, and every link must be handled.
	if digest(r.bodies(j)) != digest(plan.Programs) {
		return r.failed(ctx, s, j, "程序目录与确认时不同", nil)
	}
	links, linkBytes, err := actionjournal.ScanLinks(plan.Live, plan.Programs)
	if err != nil {
		return r.failed(ctx, s, j, "停服后状态目录里有无法处理的链接", err)
	}
	if len(links) > 0 && p.LinkHandling < action.LinkHandlingVersion {
		return r.failed(ctx, s, j, "停服后状态目录里出现需要特别处理的链接，这个客户端版本不能显示处理方式", nil)
	}
	allow := actionjournal.OriginalTexts(links)
	_, size, err := actionjournal.TreeFactsAllow(plan.Live, allow)
	if err != nil {
		return r.failed(ctx, s, j, "停服后无法读取完整状态目录", err)
	}
	if err = syscall.Statfs(filepath.Dir(plan.Live), &disk); err != nil || uint64(disk.Bavail)*uint64(disk.Bsize) < uint64(size+linkBytes)+16<<20 {
		return r.failed(ctx, s, j, "可用空间不足以复制完整状态目录", err)
	}
	j.Plan.Links, j.Plan.LinkBytes, j.Plan.Bytes = links, linkBytes, size
	if err = s.Save(j); err != nil {
		return r.failed(ctx, s, j, "无法保存停服后的目录读数", err)
	}
	plan = &j.Plan
	j.LiveTreeDigest, err = actionjournal.TreeDigestAllow(plan.Live, allow)
	if err != nil {
		return r.failed(ctx, s, j, "无法固定活动目录内容", err)
	}
	j.Phase = "copying"
	if err = s.Save(j); err != nil {
		return r.failed(ctx, s, j, "无法保存复制意图", err)
	}
	_, err = actionjournal.CopyTree(plan.Live, j.Stage, allow, func(id action.Identity) error { j.StageIdentity = id; return s.Save(j) })
	if err != nil {
		return r.failed(ctx, s, j, "完整状态目录复制失败", err)
	}
	copied, copyErr := actionjournal.TreeDigestAllow(j.Stage, allow)
	if copyErr != nil || copied != j.LiveTreeDigest {
		return r.failed(ctx, s, j, "复制期间活动内容发生变化", copyErr)
	}
	stagedLinks, err := actionjournal.StageLinks(j.Stage, plan.Live, filepath.Dir(j.Stage), plan.Links)
	if err != nil {
		return r.failed(ctx, s, j, "暂存中的链接无法改为指向暂存", err)
	}
	staged := stageEnvironment(e, j)
	before, _, err := actionjournal.TreeFactsAllow(j.Stage, stagedLinks)
	if err != nil {
		return r.failed(ctx, s, j, "无法核实暂存文件", err)
	}
	areas := plan.Definition.Transaction.Areas()
	if err = actionjournal.CheckWrites(j.Stage, areas); err != nil {
		return r.failed(ctx, s, j, "暂存写入目标无法隔离", err)
	}
	for _, step := range plan.Definition.Steps {
		j.Phase = "step-started"
		j.Steps = append(j.Steps, action.StepResult{ID: step.ID, Started: true})
		if err = s.Save(j); err != nil {
			return r.failed(ctx, s, j, "无法保存步骤开始记录", err)
		}
		argv, ok := staged.ExpandArgv(step.Argv, nil)
		if !ok {
			return r.failed(ctx, s, j, "步骤环境不完整", nil)
		}
		_, code, runErr := command(ctx, staged, lock, j.Stage, argv, false)
		result := &j.Steps[len(j.Steps)-1]
		result.Exit = &code
		result.Completed = runErr == nil
		if err = s.Save(j); err != nil {
			return r.failed(ctx, s, j, "无法保存步骤结果", err)
		}
		if runErr != nil || code != 0 {
			return r.failed(ctx, s, j, "配置步骤失败："+step.ID, runErr)
		}
	}
	written, err := actionjournal.WriteAreaLinks(j.Stage, plan.Live, filepath.Dir(j.Stage), areas, stagedLinks, plan.Programs)
	if err != nil {
		return r.failed(ctx, s, j, "配置命令在写入区域留下了不能放行的链接", err)
	}
	after, _, err := actionjournal.TreeFactsAllow(j.Stage, actionjournal.Merged(stagedLinks, written))
	if err != nil {
		return r.failed(ctx, s, j, "暂存目录包含无法隔离的文件", err)
	}
	if err = actionjournal.CheckChanges(before, after, areas); err != nil {
		reason := "配置命令写入未声明的目标"
		var u *actionjournal.UndeclaredWrites
		if errors.As(err, &u) {
			reason += "：" + strings.Join(u.Paths, "、")
		}
		return r.failed(ctx, s, j, reason, err)
	}
	if err = actionjournal.CheckPrograms(filepath.Dir(j.Stage), plan.Links); err != nil {
		return r.failed(ctx, s, j, "配置命令改动了程序代码", err)
	}
	if err = actionjournal.CheckWrites(j.Stage, areas); err != nil {
		return r.failed(ctx, s, j, "配置命令产生链接写入目标", err)
	}
	matched, verifyErr := verify(ctx, staged, lock, j.Stage, &plan.Approval)
	if !matched || verifyErr != nil {
		return r.failed(ctx, s, j, "暂存审批检查未达到声明目标", verifyErr)
	}
	// The approval read may rewrite only the scratch areas (INSTALL.md 5.2.1).
	if written, err = actionjournal.WriteAreaLinks(j.Stage, plan.Live, filepath.Dir(j.Stage), areas, stagedLinks, plan.Programs); err != nil {
		return r.failed(ctx, s, j, "审批读法在写入区域留下了不能放行的链接", err)
	}
	scratch := plan.Definition.Transaction.Scratch
	checked, _, err := actionjournal.TreeFactsAllow(j.Stage, actionjournal.Merged(stagedLinks, written))
	if err != nil || digest(actionjournal.WithoutAreas(checked, scratch)) != digest(actionjournal.WithoutAreas(after, scratch)) {
		return r.failed(ctx, s, j, "审批读法意外改变暂存配置", err)
	}
	if err = actionjournal.CheckPrograms(filepath.Dir(j.Stage), plan.Links); err != nil {
		return r.failed(ctx, s, j, "审批读法改动了程序代码", err)
	}
	if err = actionjournal.RestoreLinks(j.Stage, plan.Links, stagedLinks, areas); err != nil {
		return r.failed(ctx, s, j, "暂存中的链接无法还原为原文本", err)
	}
	j.WriteLinks = written
	j.StageTreeDigest, err = actionjournal.TreeDigestAllow(j.Stage, actionjournal.Merged(allow, written))
	if err != nil {
		return r.failed(ctx, s, j, "暂存配置无法固定", err)
	}
	j.StageVerified = true
	j.Phase = "ready"
	if err = s.Save(j); err != nil {
		return r.failed(ctx, s, j, "无法保存暂存复核结果", err)
	}
	if err = r.idle(j.Plan.Live, j.Stage); err != nil {
		return r.failed(ctx, s, j, "配置目录仍被使用", err)
	}
	if err = s.Publish(j); err != nil {
		return r.failed(ctx, s, j, "配置发布未完成", err)
	}
	return r.finish(ctx, e, lock, s, j)
}

// bodies are the recorded program locations of the operation's instance.
func (r Runner) bodies(j *action.Operation) []string {
	rec, err := porterenroll.ReadRecord(r.Env.Home, j.Plan.InstanceID)
	if err != nil || rec == nil {
		return nil
	}
	return rec.Provenance.BodyPaths()
}

func (r Runner) environment(j *action.Operation) (*porterenroll.Env, error) {
	rec, err := porterenroll.ReadRecord(r.Env.Home, j.Plan.InstanceID)
	if err != nil || rec == nil {
		return nil, reject("无法读取原实例")
	}
	if digest(rec.Env) != j.Plan.SelectionDigest {
		return nil, reject("实例选择变量发生变化")
	}
	root := rec.Roots[profile.StateRoot]
	parent, parentErr := filepath.EvalSymlinks(filepath.Dir(root))
	if parentErr != nil || !filepath.IsAbs(root) || filepath.Join(parent, filepath.Base(root)) != j.Plan.Live {
		return nil, reject("实例活动目录声明发生变化")
	}
	e := r.Env.For(&profile.Profile{Detect: &profile.Detect{BinDirs: j.Plan.BinDirs}}).With(rec.Env)
	return e, nil
}
func stageEnvironment(e *porterenroll.Env, j *action.Operation) *porterenroll.Env {
	vars := map[string]string{}
	for k, v := range j.Plan.Definition.Transaction.Env {
		vars[k] = strings.Replace(v, "${STAGE_DIR}", j.Stage, 1)
	}
	return e.With(vars)
}
func verify(ctx context.Context, e *porterenroll.Env, lock *os.File, dir string, c *profile.ApprovalCheck) (bool, error) {
	argv, ok := e.ExpandArgv(c.Argv, nil)
	if !ok {
		return false, reject("审批检查环境不完整")
	}
	out, code, err := command(ctx, e, lock, dir, argv, true)
	if err != nil || code != 0 {
		return false, err
	}
	if profile.RepairMatches(c, out) {
		return true, nil
	}
	if mode, _ := profile.EvalApproval(c, out); mode != profile.ApprovalUnknown {
		return false, errRepairRemaining
	}
	return false, nil
}
func (r Runner) failed(ctx context.Context, s actionjournal.Store, j *action.Operation, reason string, cause error) (*action.RunResult, error) {
	j.Failure = reason
	if err := s.Save(j); err != nil {
		return &action.RunResult{Exit: -1, Operation: j}, err
	}
	if errors.Is(cause, errChildAlive) {
		j.RecoveryFailure = "配置子进程仍存活，不能恢复服务"
		_ = s.Save(j)
		return &action.RunResult{Exit: -1, Operation: j}, nil
	}
	recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 90*time.Second)
	defer cancel()
	e, err := r.environment(j)
	if err == nil {
		if j.Phase == "move-live-intent" || j.Phase == "publish-intent" {
			err = s.Reconcile(j)
		}
		if err == nil {
			id, identityErr := actionjournal.DirectoryIdentity(j.Plan.Live)
			expected := j.Plan.LiveIdentity
			if j.Published {
				expected = j.StageIdentity
			}
			if identityErr != nil || id != expected {
				err = reject("活动目录身份发生变化")
			}
		}
		if err == nil && !j.Published && j.LiveTreeDigest != "" {
			var tree string
			tree, err = actionjournal.TreeDigestAllow(j.Plan.Live, actionjournal.OriginalTexts(j.Plan.Links))
			if err == nil && tree != j.LiveTreeDigest {
				err = reject("活动配置已被外部修改，保留现场")
			}
		}
		if err == nil {
			err = restoreServices(recovery, e, s, j)
		}
	}
	if err != nil {
		j.RecoveryFailure = "未能恢复原服务状态；查看操作记录后重试"
		j.Services = "remaining"
	} else {
		j.Done = true
		j.Phase = "abandoned"
		if j.Published {
			j.Phase = "published-with-failure"
		}
	}
	if err = s.Save(j); err != nil {
		return &action.RunResult{Exit: -1, Operation: j}, err
	}
	return &action.RunResult{Exit: -1, Operation: j}, nil
}
func (r Runner) finish(ctx context.Context, e *porterenroll.Env, lock *os.File, s actionjournal.Store, j *action.Operation) (*action.RunResult, error) {
	if err := restoreServices(ctx, e, s, j); err != nil {
		return r.failed(ctx, s, j, "恢复原服务状态失败", err)
	}
	matched, verifyErr := verify(ctx, e, lock, j.Plan.Live, &j.Plan.Approval)
	if errors.Is(verifyErr, errChildAlive) {
		return r.failed(ctx, s, j, "活动审批检查有存活子进程", verifyErr)
	}
	if matched {
		j.Approval = "resolved"
	} else if errors.Is(verifyErr, errRepairRemaining) {
		j.Approval = "remaining"
	} else {
		j.Approval = "unknown"
	}
	j.Health = "not-running"
	for _, sv := range j.Plan.Services {
		if sv.Original != "active" {
			continue
		}
		j.Health = "remaining"
		healthCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		for {
			if porterrevive.HealthOnce(healthCtx, e, profile.Profile{Health: j.Plan.Health, Entries: j.Plan.Entries}, sv.Unit) {
				j.Health = "healthy"
				break
			}
			select {
			case <-healthCtx.Done():
			case <-time.After(time.Second):
				continue
			}
			break
		}
		cancel()
		if j.Health != "healthy" {
			j.Failure = "配置已发布、原服务状态已恢复，但健康检查未通过"
			break
		}
	}
	j.Done = true
	j.Phase = "complete"
	exit := 0
	if j.Health == "remaining" {
		exit = -1
		j.Phase = "published-with-failure"
	}
	if j.Approval != "resolved" {
		j.Failure = "已发布配置，但审批复核未通过"
		j.Phase = "published-with-failure"
		exit = -1
	}
	if err := s.Save(j); err != nil {
		return &action.RunResult{Exit: exit, Operation: j}, err
	}
	return &action.RunResult{Exit: exit, Operation: j}, nil
}
