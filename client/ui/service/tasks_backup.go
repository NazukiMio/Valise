package service

import (
	"context"
	"slices"
	"sort"
	"time"

	"valise/core/protection"
	"valise/core/valise"
)

type backupFollowers = taskHub[BackupTask]

const maxBackupPoll = 30 * time.Second

func (c *BackupController) pollInterval() time.Duration {
	if c.config.PollInterval > 0 {
		return c.config.PollInterval
	}
	return 2 * time.Second
}

// supportsAsync is decided before the durable running boundary. Unknown
// capabilities keep the synchronous backup-runs/1 behaviour.
func (c *BackupController) supportsAsync(ctx context.Context, host string) bool {
	if c.config.Capabilities == nil {
		return false
	}
	caps, err := c.config.Capabilities(ctx, host)
	return err == nil && slices.Contains(caps, protection.BackupCapabilityV2)
}

// drive follows one asynchronous task to a terminal record. With send it
// first issues the single protection_backup call of this task; every other
// request is a run status query, so no path can replay the backup.
func (c *BackupController) drive(t BackupTask, send bool, stop <-chan struct{}) {
	ctx := c.follow.ctx
	type result struct {
		run protection.BackupRun
		err error
	}
	var sent chan result
	if send {
		sent = make(chan result, 1)
		go func() {
			var run protection.BackupRun
			err := c.config.RPC(ctx, t.Host, protection.MethodBackup, t.params(), &run)
			sent <- result{run, err}
		}()
	}
	var callErr error
	confirmed := false
	interval := c.pollInterval()
	defer func() {
		// The call failed and no record was observed: the result is unknown.
		if callErr != nil && !confirmed {
			_, _ = c.finish(t, protection.BackupRun{}, callErr)
		}
	}()
	for {
		// An in-flight call may be an attached backup: closing waits for it
		// to return instead of cutting the connection.
		stopping := stop
		if sent != nil {
			stopping = nil
		}
		select {
		case r := <-sent:
			sent = nil
			if r.err != nil {
				callErr = r.err
				continue
			}
			confirmed = true
			cur, err := c.finish(t, r.run, nil)
			if err != nil || backupTerminal(cur.State) {
				return
			}
		case <-time.After(interval):
			var run protection.BackupRun
			err := c.config.RPC(ctx, t.Host, protection.MethodRunStatus, protection.BackupStatusParams{AgentRef: t.AgentRef, RunID: t.RunID}, &run)
			if err != nil {
				if ue, ok := valise.As(err); ok && ue.Code == "run_not_found" && sent == nil && callErr != nil {
					return
				}
				interval = min(interval*2, maxBackupPoll)
				continue
			}
			interval = c.pollInterval()
			// Before an attached executor takes its run lock the record can
			// read as unknown; only a finished call makes that final.
			if sent != nil && run.State == "reconciling" {
				continue
			}
			confirmed = true
			cur, ferr := c.finish(t, run, nil)
			if ferr != nil || backupTerminal(cur.State) || run.State == "reconciling" {
				return
			}
		case <-stopping:
			return
		case <-ctx.Done():
			return
		}
	}
}

// ListBackupTasks returns the private index, newest observation first. It
// does not contact servers; WatchBackupTask or GetBackupTask query them.
func (c *BackupController) ListBackupTasks(context.Context) ([]BackupTask, error) {
	var tasks []BackupTask
	err := c.withStore(func() error { var e error; tasks, e = c.loadTasks(); return e })
	if err != nil {
		return nil, err
	}
	sort.Slice(tasks, func(i, j int) bool {
		if !tasks[i].ObservedAt.Equal(tasks[j].ObservedAt) {
			return tasks[i].ObservedAt.After(tasks[j].ObservedAt)
		}
		return tasks[i].TaskRef.TaskID < tasks[j].TaskRef.TaskID
	})
	return cloneProtection(tasks), nil
}

func (c *BackupController) loadRef(ref ProtectionTaskRef) (BackupTask, error) {
	if ref.ExecutorID != c.config.ExecutorID || !protection.ValidID(ref.TaskID) {
		return BackupTask{}, protectionError("request_invalid", "备份任务引用不属于当前执行环境")
	}
	var t BackupTask
	err := c.withStore(func() error { var e error; t, e = c.load(ref.TaskID); return e })
	return t, err
}

// WatchBackupTask delivers the current view, then each change until a
// terminal view closes the channel. Unsubscribing never cancels the task.
// After a restart an asynchronous task is followed again by status queries
// only.
func (c *BackupController) WatchBackupTask(_ context.Context, ref ProtectionTaskRef) (<-chan BackupTask, func(), error) {
	t, err := c.loadRef(ref)
	if err != nil {
		return nil, nil, err
	}
	ch, unsubscribe := c.follow.watch(cloneProtection(t))
	if t.Async && (t.State == "running" || t.State == "cancelling") {
		c.follow.start(t.TaskRef.TaskID, func(stop <-chan struct{}) { c.drive(t, false, stop) })
	}
	return ch, unsubscribe, nil
}

// CancelBackupTask asks the server executor to stop at its next safe point.
// The task reads cancelling until the run record confirms the outcome; a
// committed snapshot is kept. Unknown results cannot be cancelled.
func (c *BackupController) CancelBackupTask(ctx context.Context, ref ProtectionTaskRef) (BackupTask, error) {
	t, err := c.loadRef(ref)
	if err != nil {
		return t, err
	}
	switch t.State {
	case "waiting":
		return c.updateWaiting(t, func(t *BackupTask) { t.State = "cancelled"; t.Wait = "" })
	case "running", "cancelling":
	default:
		if backupTerminal(t.State) {
			return cloneProtection(t), nil
		}
		return cloneProtection(t), valise.New("result_unknown", "备份结果尚未核实，不能取消", "先查询运行状态；结果未知时请勿重发备份")
	}
	if !c.supportsAsync(ctx, t.Host) {
		return cloneProtection(t), valise.New("unsupported_capability", "服务器 porter 不支持取消进行中的备份", "等待本次备份结束，或更新服务器 porter 后再使用取消")
	}
	marked := false
	err = c.withStore(func() error {
		current, err := c.load(t.TaskRef.TaskID)
		if err != nil {
			return err
		}
		t = current
		if t.State != "running" && t.State != "cancelling" {
			return nil
		}
		t.State = "cancelling"
		t.CancelRequested = true
		t.Revision++
		t.ObservedAt = c.now().UTC()
		marked = true
		return c.save(t)
	})
	if err != nil {
		return cloneProtection(t), err
	}
	if !marked {
		return cloneProtection(t), nil
	}
	c.follow.notify(t)
	var run protection.BackupRun
	if err := c.config.RPC(ctx, t.Host, protection.MethodRunCancel, protection.BackupCancelParams{AgentRef: t.AgentRef, RunID: t.RunID}, &run); err != nil {
		return cloneProtection(t), err
	}
	t, err = c.finish(t, run, nil)
	if err == nil && t.Async && !backupTerminal(t.State) {
		c.follow.start(t.TaskRef.TaskID, func(stop <-chan struct{}) { c.drive(t, false, stop) })
	}
	return t, err
}

// Shutdown stops new backups and waits until in-flight calls return: an
// attached backup reaches its own end. Status followers stop at once; a
// detached run continues on the server and is queried after restart.
func (c *BackupController) Shutdown(ctx context.Context) error {
	return c.follow.shutdown(ctx)
}
