package service

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"valise/core/identity"
	"valise/core/protection"
	"valise/core/valise"
)

// ProtectionRPC must enforce the method's access tier and remote capability.
// It must not retry a modifying call after a transport failure.
type ProtectionRPC func(context.Context, string, string, any, any) error

// ProtectionReview is installed only by a trusted in-process human adapter.
// Neither this callback nor its answer is accepted from JSON or disk.
type ProtectionReview func(context.Context, string, protection.FrozenChange) (bool, error)

type ProtectionControllerConfig struct {
	StateDir   string
	ExecutorID string
	RPC        ProtectionRPC
	Review     ProtectionReview
}

type ProtectionController struct {
	mu      sync.Mutex
	storeMu sync.Mutex
	config  ProtectionControllerConfig
	plans   map[string]protectionPlan
	now     func() time.Time
}
type protectionPlan struct {
	host string
	plan protection.FrozenChange
	// expires is the plan's ten-minute window on this client's clock,
	// counted from receipt. The server's CreatedAt/ExpiresAt are on the
	// server clock, which may differ; the server enforces those itself.
	expires  time.Time
	consumed bool
}

type ProtectionTaskRef struct {
	ExecutorID string `json:"executor_id"`
	TaskID     string `json:"task_id"`
}
type ProtectionTaskError struct {
	Code       string `json:"code"`
	Reason     string `json:"reason"`
	Suggestion string `json:"suggestion"`
	Retry      string `json:"retry"`
}

// ProtectionTask covers only the fixed protection change executor. It does not
// represent the future general task API or grant execution rights on reload.
type ProtectionTask struct {
	Schema                 string               `json:"schema"`
	TaskRef                ProtectionTaskRef    `json:"task_ref"`
	Kind                   string               `json:"kind"`
	Host                   string               `json:"host"`
	AgentRef               identity.AgentRef    `json:"agent_ref"`
	PlanID                 string               `json:"plan_id"`
	PlanDigest             string               `json:"plan_digest"`
	RequestDigest          string               `json:"request_digest"`
	RemoteRequestDigest    string               `json:"remote_request_digest"`
	SubmissionKeyDigest    string               `json:"submission_key_digest"`
	ChangeID               string               `json:"change_id"`
	ExpectedServerRevision protection.Revision  `json:"expected_server_revision"`
	State                  string               `json:"state"`
	Wait                   string               `json:"wait,omitempty"`
	Revision               protection.Revision  `json:"revision"`
	ObservedAt             time.Time            `json:"observed_at"`
	Change                 *protection.Change   `json:"change,omitempty"`
	Error                  *ProtectionTaskError `json:"error,omitempty"`
}

func NewProtectionController(config ProtectionControllerConfig) (*ProtectionController, error) {
	if config.StateDir == "" || config.RPC == nil {
		return nil, protectionError("request_invalid", "保护计划服务缺少状态目录或连接适配")
	}
	c := &ProtectionController{config: config, plans: map[string]protectionPlan{}, now: time.Now}
	if err := c.initialize(); err != nil {
		return nil, err
	}
	return c, nil
}

func protectionError(code, reason string) error {
	return valise.New(code, reason, "重新读取保护设置并准备计划；结果未知时先核实事务状态")
}
func protectionTaskError(code, reason, retry string) *ProtectionTaskError {
	return &ProtectionTaskError{Code: code, Reason: reason, Suggestion: "重新读取保护设置并核实事务状态", Retry: retry}
}

func (c *ProtectionController) GetProtection(ctx context.Context, host string, req protection.GetParams) (protection.View, error) {
	if err := protection.ValidateAgent(req.AgentRef); err != nil {
		return protection.View{}, err
	}
	var view protection.View
	if err := c.config.RPC(ctx, host, protection.MethodGet, req, &view); err != nil {
		return protection.View{}, err
	}
	if view.Mode != "legacy" && view.Mode != "accepted" {
		return protection.View{}, protectionError("unsupported_schema", "保护计划读取模式不受支持")
	}
	if view.Mode == "accepted" {
		if view.ServerPlan == nil {
			return protection.View{}, protectionError("record_corrupt", "保护计划读取结果缺少已接受的计划")
		}
		if err := view.ServerPlan.Validate(); err != nil {
			return protection.View{}, err
		}
		if view.ServerPlan.AgentRef != req.AgentRef {
			return protection.View{}, protectionError("identity_conflict", "保护计划不属于所选实例世代")
		}
	} else if view.ServerPlan != nil {
		return protection.View{}, protectionError("record_corrupt", "旧保护设置与已接受计划状态矛盾")
	}
	return view, nil
}

func (c *ProtectionController) PrepareProtectionChange(ctx context.Context, host string, req protection.PrepareParams) (protection.FrozenChange, error) {
	if host == "" {
		return protection.FrozenChange{}, protectionError("request_invalid", "缺少目标服务器")
	}
	if err := req.Validate(); err != nil {
		return protection.FrozenChange{}, err
	}
	var plan protection.FrozenChange
	if err := c.config.RPC(ctx, host, protection.MethodPrepare, req, &plan); err != nil {
		return plan, err
	}
	if err := c.validatePlan(plan, req); err != nil {
		return protection.FrozenChange{}, err
	}
	// Keep private copies of pointer/slice members; a DTO cannot modify a plan.
	plan = cloneProtection(plan)
	c.mu.Lock()
	for id, p := range c.plans {
		if !c.now().Before(p.expires) {
			delete(c.plans, id)
		}
	}
	if _, exists := c.plans[plan.PlanID]; exists {
		c.mu.Unlock()
		return protection.FrozenChange{}, protectionError("source_conflict", "服务器重复使用了计划标识")
	}
	c.plans[plan.PlanID] = protectionPlan{host: host, plan: plan, expires: c.now().Add(plan.ExpiresAt.Sub(plan.CreatedAt))}
	c.mu.Unlock()
	return cloneProtection(plan), nil
}
func (c *ProtectionController) validatePlan(plan protection.FrozenChange, req protection.PrepareParams) error {
	digest, err := plan.Digest()
	a, _ := protection.Digest(req)
	b, _ := protection.Digest(plan.Request)
	// Freshness is judged by the window length only: the timestamps are on
	// the server clock, which may run ahead of or behind this client.
	if err != nil || digest != plan.PlanDigest || a != b || !protection.ValidID(plan.PlanID) || plan.Kind != "protection_change" || plan.PreconditionsDigest == "" || plan.ExpiresAt.Sub(plan.CreatedAt) != 10*time.Minute {
		return protectionError("plan_stale", "服务器返回的冻结计划无效或已过期")
	}
	return nil
}
func cloneProtection[T any](v T) T {
	b, _ := json.Marshal(v)
	var out T
	_ = json.Unmarshal(b, &out)
	return out
}

// StartProtectionChange accepts only the identity of an in-memory prepared plan.
// Reusing a key returns its existing task. Waiting tasks can be reviewed again;
// running or uncertain tasks are never sent to Apply a second time.
func (c *ProtectionController) StartProtectionChange(ctx context.Context, planID, submissionKey string) (ProtectionTask, error) {
	if err := ctx.Err(); err != nil {
		return ProtectionTask{}, err
	}
	if submissionKey == "" || len(submissionKey) > 256 {
		return ProtectionTask{}, protectionError("request_invalid", "提交标识为空或过长")
	}
	c.mu.Lock()
	p, ok := c.plans[planID]
	c.mu.Unlock()
	if !ok {
		return ProtectionTask{}, protectionError("plan_stale", "计划不在当前进程中，请重新准备")
	}
	requestDigest, _ := protection.Digest(struct {
		Host    string
		Request protection.PrepareParams
	}{p.host, p.plan.Request})
	keyDigest, _ := protection.Digest(submissionKey)
	var task ProtectionTask
	err := c.withStore(func() error {
		tasks, err := c.loadTasks()
		if err != nil {
			return err
		}
		for _, t := range tasks {
			if t.SubmissionKeyDigest == keyDigest {
				if t.RequestDigest != requestDigest {
					return protectionError("request_conflict", "同一提交标识已经用于另一项请求")
				}
				task = t
				return nil
			}
		}
		if p.consumed {
			return protectionError("plan_stale", "此计划的确认已被消费，请重新准备")
		}
		if !c.now().Before(p.expires) {
			return protectionError("plan_expired", "首次执行的十分钟有效期已结束")
		}
		taskID, err := randomJourneyID()
		if err != nil {
			return err
		}
		changeID, err := randomJourneyID()
		if err != nil {
			return err
		}
		remoteDigest, _ := protection.Digest(p.plan.Request)
		task = ProtectionTask{Schema: "valise-protection-task/1", TaskRef: ProtectionTaskRef{c.config.ExecutorID, taskID}, Kind: "protection_change", Host: p.host, AgentRef: p.plan.Request.AgentRef, PlanID: planID, PlanDigest: p.plan.PlanDigest, RequestDigest: requestDigest, RemoteRequestDigest: remoteDigest, SubmissionKeyDigest: keyDigest, ChangeID: changeID, ExpectedServerRevision: p.plan.Request.ExpectedRevision, State: "waiting", Wait: "confirmation", Revision: 1, ObservedAt: c.now().UTC()}
		return c.saveTask(task)
	})
	if err != nil {
		return task, err
	}
	if task.State != "waiting" {
		return cloneProtection(task), nil
	}
	if c.config.Review == nil {
		return cloneProtection(task), nil
	}
	if p.consumed || !c.now().Before(p.expires) {
		return task, protectionError("plan_expired", "计划确认已失效，请重新准备")
	}
	// Human interaction never holds the disk index lock.
	accepted, err := c.config.Review(ctx, p.host, cloneProtection(p.plan))
	if err != nil {
		return task, err
	}
	if !accepted {
		return c.updateWaiting(task, func(t *ProtectionTask) { t.State = "cancelled"; t.Wait = "" })
	}
	if err := ctx.Err(); err != nil {
		return task, err
	}
	if !c.now().Before(p.expires) {
		return task, protectionError("plan_expired", "确认期间计划已过期，请重新准备")
	}
	// Re-read facts after confirmation. A new remote plan has different random
	// identity/time; only its requested choices and factual preconditions bind us.
	var fresh protection.FrozenChange
	if err := c.config.RPC(ctx, p.host, protection.MethodPrepare, p.plan.Request, &fresh); err != nil {
		return task, err
	}
	if err := c.validatePlan(fresh, p.plan.Request); err != nil {
		return task, err
	}
	if fresh.PreconditionsDigest != p.plan.PreconditionsDigest || fresh.CalendarPreview != p.plan.CalendarPreview {
		return task, protectionError("plan_stale", "确认后服务器身份、版本或设置已变化")
	}
	claimed := false
	c.mu.Lock()
	currentPlan, exists := c.plans[planID]
	if !exists || currentPlan.consumed || !c.now().Before(p.expires) {
		c.mu.Unlock()
		return task, protectionError("plan_stale", "此计划已过期或已被另一项任务消费")
	}
	task, err = c.updateWaiting(task, func(t *ProtectionTask) {
		t.State = "running"
		t.Wait = ""
		t.PlanID = planID
		t.PlanDigest = p.plan.PlanDigest
		t.Error = nil
		claimed = true
	})
	if claimed {
		currentPlan.consumed = true
		c.plans[planID] = currentPlan
	}
	c.mu.Unlock()
	if err != nil || !claimed {
		return task, err
	}
	// The durable running record is the point after which even a crash before
	// delivery must reconcile using change_id instead of guessing and resending.
	var change protection.Change
	callErr := c.config.RPC(ctx, p.host, protection.MethodApply, protection.ApplyParams{Plan: p.plan, ChangeID: task.ChangeID}, &change)
	return c.finishTask(task, change, callErr)
}

func (c *ProtectionController) updateWaiting(old ProtectionTask, update func(*ProtectionTask)) (ProtectionTask, error) {
	task := old
	err := c.withStore(func() error {
		current, err := c.loadTask(old.TaskRef.TaskID)
		if err != nil {
			return err
		}
		task = current
		if current.State != "waiting" || current.Revision != old.Revision {
			return nil
		}
		update(&task)
		task.Revision++
		task.ObservedAt = c.now().UTC()
		return c.saveTask(task)
	})
	return cloneProtection(task), err
}

// GetTask reads a private index and, for an uncertain/running change, queries the
// original remote transaction. Missing status is unknown, never permission to retry.
func (c *ProtectionController) GetTask(ctx context.Context, ref ProtectionTaskRef) (ProtectionTask, error) {
	if err := ctx.Err(); err != nil {
		return ProtectionTask{}, err
	}
	if ref.ExecutorID != c.config.ExecutorID || !protection.ValidID(ref.TaskID) {
		return ProtectionTask{}, protectionError("request_invalid", "任务引用不属于此客户端执行环境")
	}
	var task ProtectionTask
	err := c.withStore(func() error { var e error; task, e = c.loadTask(ref.TaskID); return e })
	if err != nil {
		return task, err
	}
	switch task.State {
	case "running", "reconciling", "needs_recovery":
	default:
		return cloneProtection(task), nil
	}
	var change protection.Change
	callErr := c.config.RPC(ctx, task.Host, protection.MethodStatus, protection.StatusParams{AgentRef: task.AgentRef, ChangeID: task.ChangeID}, &change)
	return c.finishTask(task, change, callErr)
}

func (c *ProtectionController) finishTask(old ProtectionTask, change protection.Change, callErr error) (ProtectionTask, error) {
	task := old
	err := c.withStore(func() error {
		current, err := c.loadTask(old.TaskRef.TaskID)
		if err != nil {
			return err
		}
		task = current
		// A slower observation must not downgrade a known terminal outcome.
		if task.State == "succeeded" || task.State == "failed" || task.State == "cancelled" {
			return nil
		}
		if callErr != nil || change.Schema != "valise-protection-change/1" || change.ChangeID != task.ChangeID || change.AgentRef != task.AgentRef || change.RequestDigest != task.RemoteRequestDigest || change.OldRevision != task.ExpectedServerRevision || change.NewRevision <= change.OldRevision || change.NewRevision != change.OldRevision+1 {
			task.State = "reconciling"
			task.Error = protectionTaskError("result_unknown", "保护设置请求的结果尚未核实，不能自动重发", "reconcile")
		} else {
			task.Change = &change
			task.Error = nil
			switch {
			case change.State == "committed" && change.Committed && !change.ReconcileRequired:
				task.State = "succeeded"
			case change.State == "failed" && change.RollbackSucceeded && !change.Committed && !change.ReconcileRequired:
				task.State = "failed"
				task.Error = protectionTaskError("protection_change_failed", "保护设置未完成，旧设置已恢复", "replan")
			default:
				task.State = "needs_recovery"
				task.Error = protectionTaskError("result_unknown", "保护事务保留未完成记录，需要核实", "reconcile")
			}
		}
		task.Revision++
		task.ObservedAt = c.now().UTC()
		return c.saveTask(task)
	})
	return cloneProtection(task), err
}
