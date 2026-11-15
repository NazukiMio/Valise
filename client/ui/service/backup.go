package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"valise/core/identity"
	"valise/core/protection"
)

// BackupReview is installed by a trusted in-process human interface. Neither
// this callback nor its answer may be supplied by JSON or a persisted task.
type BackupReview func(context.Context, string, BackupPlan) (bool, error)
type BackupControllerConfig struct {
	StateDir   string
	ExecutorID string
	RPC        ProtectionRPC
	Review     BackupReview
	// Capabilities returns the porter's hello capabilities for host. Only a
	// porter declaring backup-runs/2 gets the asynchronous Start; nil, an
	// error or an older porter keeps the synchronous backup-runs/1 call.
	Capabilities func(ctx context.Context, host string) ([]string, error)
	// PollInterval spaces run status queries of asynchronous tasks.
	PollInterval time.Duration
	AllowOneOff  func(context.Context, string) error
}
type BackupPlan struct {
	Selection           protection.BackupSelection `json:"selection,omitzero"`
	PlanID              string                     `json:"plan_id"`
	Kind                string                     `json:"kind"`
	CreatedAt           time.Time                  `json:"created_at"`
	ExpiresAt           time.Time                  `json:"expires_at"`
	ServerPlan          protection.ServerPlan      `json:"server_plan"`
	IncludesCredentials bool                       `json:"includes_credentials"`
	PlanDigest          string                     `json:"plan_digest"`
}
type BackupTask struct {
	Selection           protection.BackupSelection `json:"selection,omitzero"`
	Schema              string                     `json:"schema"`
	TaskRef             ProtectionTaskRef          `json:"task_ref"`
	Kind                string                     `json:"kind"`
	Host                string                     `json:"host"`
	AgentRef            identity.AgentRef          `json:"agent_ref"`
	PlanID              string                     `json:"plan_id"`
	PlanDigest          string                     `json:"plan_digest"`
	RequestDigest       string                     `json:"request_digest"`
	SubmissionKeyDigest string                     `json:"submission_key_digest"`
	RunID               string                     `json:"run_id"`
	ServerPlan          protection.ServerPlan      `json:"server_plan"`
	State               string                     `json:"state"`
	Wait                string                     `json:"wait,omitempty"`
	Revision            protection.Revision        `json:"revision"`
	ObservedAt          time.Time                  `json:"observed_at"`
	Run                 *protection.BackupRun      `json:"run,omitempty"`
	Error               *ProtectionTaskError       `json:"error,omitempty"`
	// Hint is a quiet note for the person, not part of the task's record:
	// with 分离模式 off, it says the backup could run in the background.
	Hint string `json:"hint,omitempty"`
	// Async marks a backup-runs/2 task: Start returned before completion and
	// the run is followed by status queries. Execution mirrors the run
	// record; attached means closing Valise interrupts this backup.
	Async     bool   `json:"async,omitempty"`
	Execution string `json:"execution,omitempty"`
	// CancelRequested is set once backup_run_cancel was sent; the state is
	// cancelling until the run record confirms a stop or a final result.
	CancelRequested bool `json:"cancel_requested,omitempty"`
}
type backupPrepared struct {
	host     string
	plan     BackupPlan
	consumed bool
}
type BackupController struct {
	mu      sync.Mutex
	storeMu sync.Mutex
	config  BackupControllerConfig
	plans   map[string]backupPrepared
	now     func() time.Time
	follow  backupFollowers
}

func NewBackupController(config BackupControllerConfig) (*BackupController, error) {
	if config.StateDir == "" || config.RPC == nil {
		return nil, protectionError("request_invalid", "备份服务缺少状态目录或连接适配")
	}
	c := &BackupController{config: config, plans: map[string]backupPrepared{}, now: time.Now}
	c.follow.init(func(t BackupTask) string { return t.TaskRef.TaskID }, func(t BackupTask) bool { return backupTerminal(t.State) })
	err := c.withStore(func() error {
		path := filepath.Join(c.dir(), ".executor.json")
		var r struct {
			ExecutorID string `json:"executor_id"`
		}
		b, err := os.ReadFile(path)
		if err == nil {
			if json.Unmarshal(b, &r) != nil || !protection.ValidID(r.ExecutorID) {
				return protectionError("record_corrupt", "备份任务执行环境身份损坏")
			}
			if config.ExecutorID != "" && config.ExecutorID != r.ExecutorID {
				return protectionError("identity_conflict", "备份任务目录属于其他执行环境")
			}
			c.config.ExecutorID = r.ExecutorID
			return nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return protectionError("record_corrupt", "无法读取备份执行环境身份")
		}
		if c.config.ExecutorID == "" {
			c.config.ExecutorID, err = identity.NewID()
			if err != nil {
				return err
			}
		}
		if !protection.ValidID(c.config.ExecutorID) {
			return protectionError("request_invalid", "执行环境标识无效")
		}
		r.ExecutorID = c.config.ExecutorID
		return protectionAtomic(path, r)
	})
	return c, err
}
func (c *BackupController) accepted(ctx context.Context, host string, a identity.AgentRef) (protection.ServerPlan, error) {
	var v protection.View
	if err := c.config.RPC(ctx, host, protection.MethodGet, protection.GetParams{AgentRef: a}, &v); err != nil {
		return protection.ServerPlan{}, err
	}
	if v.Mode != "accepted" || v.ServerPlan == nil {
		return protection.ServerPlan{}, protectionError("unsupported_capability", "实例尚未采用版本化保护计划，不能按新计划备份")
	}
	if err := v.ServerPlan.Validate(); err != nil {
		return protection.ServerPlan{}, err
	}
	if v.ServerPlan.AgentRef != a {
		return protection.ServerPlan{}, protectionError("identity_conflict", "保护计划不属于请求的实例世代")
	}
	// Runtime warnings such as linger do not prohibit manual backup.
	// WithAccepted remains the execution authority for pending transactions.
	return cloneProtection(*v.ServerPlan), nil
}
func (c *BackupController) PrepareBackup(ctx context.Context, host string, a identity.AgentRef) (BackupPlan, error) {
	if host == "" {
		return BackupPlan{}, protectionError("request_invalid", "缺少备份服务器")
	}
	if err := protection.ValidateAgent(a); err != nil {
		return BackupPlan{}, err
	}
	server, err := c.accepted(ctx, host, a)
	if err != nil {
		return BackupPlan{}, err
	}
	id, err := identity.NewID()
	if err != nil {
		return BackupPlan{}, err
	}
	now := c.now().UTC()
	plan := BackupPlan{PlanID: id, Kind: "backup", CreatedAt: now, ExpiresAt: now.Add(10 * time.Minute), ServerPlan: server, IncludesCredentials: server.Scope.Tier == "full" || server.Scope.Tier == "soul-secrets"}
	plan.PlanDigest, _ = protection.Digest(plan)
	c.mu.Lock()
	for key, p := range c.plans {
		if !now.Before(p.plan.ExpiresAt) {
			delete(c.plans, key)
		}
	}
	c.plans[id] = backupPrepared{host: host, plan: cloneProtection(plan)}
	c.mu.Unlock()
	return cloneProtection(plan), nil
}
func backupRequestDigest(host string, p protection.ServerPlan, selections ...protection.BackupSelection) string {
	d, _ := protection.Digest(struct {
		Host string
		Plan protection.ServerPlan
	}{host, p})
	if len(selections) > 0 && selections[0] != (protection.BackupSelection{}) {
		d, _ = protection.Digest(struct {
			Base      string
			Selection protection.BackupSelection
		}{d, selections[0]})
	}
	return d
}
func (c *BackupController) StartBackup(ctx context.Context, planID, submissionKey string) (BackupTask, error) {
	if err := ctx.Err(); err != nil {
		return BackupTask{}, err
	}
	if submissionKey == "" || len(submissionKey) > 256 {
		return BackupTask{}, protectionError("request_invalid", "提交标识为空或过长")
	}
	if c.follow.isClosing() {
		return BackupTask{}, protectionError("resource_busy", "Valise 正在关闭，不再开始新的备份")
	}
	c.mu.Lock()
	p, ok := c.plans[planID]
	c.mu.Unlock()
	if !ok {
		return BackupTask{}, protectionError("plan_stale", "计划不在当前进程中，请重新准备备份")
	}
	digest := backupRequestDigest(p.host, p.plan.ServerPlan, p.plan.Selection)
	keyDigest, _ := protection.Digest(submissionKey)
	var task BackupTask
	err := c.withStore(func() error {
		tasks, err := c.loadTasks()
		if err != nil {
			return err
		}
		for _, t := range tasks {
			if t.SubmissionKeyDigest == keyDigest {
				if t.RequestDigest != digest {
					return protectionError("request_conflict", "同一提交标识已经用于另一项备份请求")
				}
				task = t
				return nil
			}
		}
		if p.consumed {
			return protectionError("plan_stale", "此备份计划已经执行，请重新准备")
		}
		if !c.now().Before(p.plan.ExpiresAt) {
			return protectionError("plan_expired", "备份计划已过期，请重新准备")
		}
		taskID, err := identity.NewID()
		if err != nil {
			return err
		}
		runID, err := identity.NewID()
		if err != nil {
			return err
		}
		task = BackupTask{Selection: p.plan.Selection, Schema: "valise-backup-task/1", Kind: "backup", TaskRef: ProtectionTaskRef{ExecutorID: c.config.ExecutorID, TaskID: taskID}, Host: p.host, AgentRef: p.plan.ServerPlan.AgentRef, PlanID: planID, PlanDigest: p.plan.PlanDigest, RequestDigest: digest, SubmissionKeyDigest: keyDigest, RunID: runID, ServerPlan: cloneProtection(p.plan.ServerPlan), State: "waiting", Wait: "confirmation", Revision: 1, ObservedAt: c.now().UTC()}
		return c.save(task)
	})
	if err != nil || task.State != "waiting" || c.config.Review == nil {
		return cloneProtection(task), err
	}
	if p.consumed || !c.now().Before(p.plan.ExpiresAt) {
		return task, protectionError("plan_expired", "备份确认已失效，请重新准备")
	}
	accepted, err := c.config.Review(ctx, p.host, cloneProtection(p.plan))
	if err != nil {
		return task, err
	}
	if !accepted {
		return c.updateWaiting(task, func(t *BackupTask) { t.State = "cancelled"; t.Wait = "" })
	}
	if err := ctx.Err(); err != nil {
		return task, err
	}
	fresh, err := c.accepted(ctx, p.host, p.plan.ServerPlan.AgentRef)
	if err != nil {
		return task, err
	}
	if backupRequestDigest(p.host, fresh, p.plan.Selection) != digest {
		return task, protectionError("plan_stale", "确认后保护计划或实例身份已变化，请重新准备")
	}
	if p.plan.Selection.Tier != "" {
		if err := c.allowOneOff(ctx, p.host); err != nil {
			return task, err
		}
	}
	async := c.supportsAsync(ctx, p.host)
	c.mu.Lock()
	current, ok := c.plans[planID]
	if !ok || current.consumed || !c.now().Before(current.plan.ExpiresAt) {
		c.mu.Unlock()
		return task, protectionError("plan_stale", "备份计划已过期或已经执行")
	}
	claimed := false
	task, err = c.updateWaiting(task, func(t *BackupTask) {
		t.State = "running"
		t.Wait = ""
		t.PlanID = planID
		t.PlanDigest = p.plan.PlanDigest
		t.Async = async
		claimed = true
	})
	if claimed {
		current.consumed = true
		c.plans[planID] = current
	}
	c.mu.Unlock()
	if err != nil || !claimed {
		return task, err
	}
	// No code path reissues this call after the durable running boundary.
	if async {
		if !c.follow.start(task.TaskRef.TaskID, func(stop <-chan struct{}) { c.drive(task, true, stop) }) {
			return c.finish(task, protection.BackupRun{}, protectionError("resource_busy", "Valise 正在关闭"))
		}
		return task, nil
	}
	var run protection.BackupRun
	callErr := c.config.RPC(ctx, task.Host, protection.MethodBackup, task.params(), &run)
	return c.finish(task, run, callErr)
}
func (t BackupTask) params() protection.BackupParams {
	return protection.BackupParams{AgentRef: t.AgentRef, ExpectedRevision: t.ServerPlan.Revision, RunID: t.RunID, Reason: "manual", Detach: t.Async, Selection: t.Selection}
}
func (c *BackupController) updateWaiting(old BackupTask, update func(*BackupTask)) (BackupTask, error) {
	t := old
	err := c.withStore(func() error {
		current, err := c.load(old.TaskRef.TaskID)
		if err != nil {
			return err
		}
		t = current
		if current.State != "waiting" || current.Revision != old.Revision {
			return nil
		}
		update(&t)
		t.Revision++
		t.ObservedAt = c.now().UTC()
		return c.save(t)
	})
	if err == nil {
		c.follow.notify(t)
	}
	return cloneProtection(t), err
}

// GetBackupTask only queries run status. Missing remote evidence never grants
// permission to resend a modifying request, including after process restart.
func (c *BackupController) GetBackupTask(ctx context.Context, ref ProtectionTaskRef) (BackupTask, error) {
	if ref.ExecutorID != c.config.ExecutorID || !protection.ValidID(ref.TaskID) {
		return BackupTask{}, protectionError("request_invalid", "备份任务引用不属于当前执行环境")
	}
	var t BackupTask
	err := c.withStore(func() error { var e error; t, e = c.load(ref.TaskID); return e })
	if err != nil {
		return t, err
	}
	switch t.State {
	case "running", "cancelling", "reconciling", "needs_recovery":
	default:
		return cloneProtection(t), nil
	}
	var run protection.BackupRun
	callErr := c.config.RPC(ctx, t.Host, protection.MethodRunStatus, protection.BackupStatusParams{AgentRef: t.AgentRef, RunID: t.RunID}, &run)
	return c.finish(t, run, callErr)
}
func (c *BackupController) finish(old BackupTask, run protection.BackupRun, callErr error) (BackupTask, error) {
	t := old
	err := c.withStore(func() error {
		current, err := c.load(old.TaskRef.TaskID)
		if err != nil {
			return err
		}
		t = current
		if backupTerminal(t.State) {
			return nil
		}
		valid := callErr == nil && backupRunMatches(t, run)
		cancelling := t.State == "cancelling" || t.CancelRequested
		t.State = "reconciling"
		t.Error = protectionTaskError("result_unknown", "备份结果尚未核实，不能自动重发", "reconcile")
		if valid {
			t.Run = &run
			t.Execution = run.Execution
			switch run.State {
			case "succeeded", "partial":
				t.State = run.State
				t.Error = nil
			case "failed":
				t.State = "failed"
				t.Error = protectionTaskError("backup_failed", "服务器备份未完成，请核实记录后重新准备", "replan")
			case "cancelled":
				// The executor confirmed it stopped; a committed snapshot is
				// kept in run.Snapshot.
				t.State = "cancelled"
				t.Error = nil
			case "accepted", "running":
				t.State = "running"
				if cancelling {
					t.State = "cancelling"
				}
				t.Error = nil
			case "reconciling":
			}
		}
		t.Revision++
		t.ObservedAt = c.now().UTC()
		return c.save(t)
	})
	if err == nil {
		c.follow.notify(t)
	}
	return cloneProtection(t), err
}
func backupTerminal(state string) bool {
	switch state {
	case "succeeded", "partial", "failed", "cancelled":
		return true
	}
	return false
}
func backupRunMatches(t BackupTask, run protection.BackupRun) bool {
	requestDigest, _ := protection.Digest(t.params())
	planDigest, _ := protection.Digest(run.Plan)
	expectedPlan, _ := protection.Digest(t.ServerPlan)
	if run.Schema != protection.BackupRunSchema || run.RunID != t.RunID || run.AgentRef != t.AgentRef || run.Request != t.params() || run.RequestDigest != requestDigest || planDigest != expectedPlan || run.AcceptedAt.IsZero() || run.UpdatedAt.IsZero() {
		return false
	}
	switch run.State {
	case "succeeded", "partial":
		return run.Snapshot != nil && run.Snapshot.VaultID != "" && run.Snapshot.SnapshotID != ""
	case "accepted", "running", "reconciling", "failed", "cancelled":
		return true
	default:
		return false
	}
}
func (c *BackupController) dir() string { return filepath.Join(c.config.StateDir, "backup-tasks") }
func (c *BackupController) withStore(fn func() error) error {
	c.storeMu.Lock()
	defer c.storeMu.Unlock()
	if err := os.MkdirAll(c.dir(), 0700); err != nil {
		return protectionError("record_corrupt", "无法创建备份任务目录")
	}
	st, err := os.Lstat(c.dir())
	if err != nil || !st.IsDir() {
		return protectionError("record_corrupt", "备份任务目录无效")
	}
	if err = os.Chmod(c.dir(), 0700); err != nil {
		return err
	}
	unlock, err := protectionStoreLock(filepath.Join(c.dir(), ".backup.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	return fn()
}
func (c *BackupController) save(t BackupTask) error {
	return protectionAtomic(filepath.Join(c.dir(), t.TaskRef.TaskID+".json"), t)
}
func (c *BackupController) load(id string) (BackupTask, error) {
	var t BackupTask
	if !protection.ValidID(id) {
		return t, protectionError("record_corrupt", "备份任务标识无效")
	}
	path := filepath.Join(c.dir(), id+".json")
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 || st.Size() > 4<<20 {
		return t, protectionError("record_corrupt", "备份任务记录缺失或不可读")
	}
	b, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(b, &t) != nil || t.Schema != "valise-backup-task/1" || t.Kind != "backup" || t.TaskRef.TaskID != id || t.TaskRef.ExecutorID != c.config.ExecutorID || !protection.ValidID(t.RunID) || t.ServerPlan.Validate() != nil || t.AgentRef != t.ServerPlan.AgentRef || t.Host == "" || t.Revision == 0 || t.SubmissionKeyDigest == "" || t.RequestDigest != backupRequestDigest(t.Host, t.ServerPlan, t.Selection) {
		return BackupTask{}, protectionError("record_corrupt", "备份任务记录格式或请求绑定无效")
	}
	if !protection.ValidID(t.PlanID) || t.PlanDigest == "" || t.ObservedAt.IsZero() || (t.Run != nil && !backupRunMatches(t, *t.Run)) {
		return BackupTask{}, protectionError("record_corrupt", "备份任务计划或运行证据绑定无效")
	}
	if t.State == "succeeded" || t.State == "partial" || t.State == "failed" || (t.State == "cancelled" && t.Run != nil) {
		if t.Run == nil || t.Run.State != t.State {
			return BackupTask{}, protectionError("record_corrupt", "备份任务缺少对应终态运行证据")
		}
	}
	switch t.State {
	case "waiting", "running", "cancelling", "reconciling", "needs_recovery", "succeeded", "partial", "failed", "cancelled":
	default:
		return BackupTask{}, protectionError("record_corrupt", "备份任务状态无效")
	}
	return t, nil
}
func (c *BackupController) loadTasks() ([]BackupTask, error) {
	es, err := os.ReadDir(c.dir())
	if err != nil {
		return nil, err
	}
	out := []BackupTask{}
	for _, e := range es {
		if strings.HasPrefix(e.Name(), ".") || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		t, err := c.load(id)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}
