package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"valise/core/identity"
	"valise/core/protection"
	"valise/core/valise"
)

// Local tasks run in this client process because their data must pass
// through this computer (pulling into a local repository, staging a restore
// upload). The index under the client state directory only records views;
// it never authorizes running a job again after a restart.

const localTaskSchema = "valise-local-task/1"

// LocalTaskRepoSync pulls a server's vault into a local repository.
const LocalTaskRepoSync = "repo_sync"

type LocalTaskSubject struct {
	Host string `json:"host,omitempty"`
	Repo string `json:"repo,omitempty"`
}
type LocalTask struct {
	Schema              string               `json:"schema"`
	TaskRef             ProtectionTaskRef    `json:"task_ref"`
	Kind                string               `json:"kind"`
	Subject             LocalTaskSubject     `json:"subject"`
	RequestDigest       string               `json:"request_digest"`
	SubmissionKeyDigest string               `json:"submission_key_digest"`
	State               string               `json:"state"`
	Stage               string               `json:"stage,omitempty"`
	CancelRequested     bool                 `json:"cancel_requested,omitempty"`
	Result              json.RawMessage      `json:"result,omitempty"`
	Error               *ProtectionTaskError `json:"error,omitempty"`
	Revision            uint64               `json:"revision"`
	CreatedAt           time.Time            `json:"created_at"`
	ObservedAt          time.Time            `json:"observed_at"`
}

// LocalJob is a fixed in-process job. It must stop at its next interruptible
// boundary when ctx ends and return a redacted result summary.
type LocalJob func(ctx context.Context, stage func(string)) (any, error)

type LocalTasks struct {
	dir        string
	executorID string
	storeMu    sync.Mutex
	mu         sync.Mutex
	cancels    map[string]func(reason string)
	hub        taskHub[LocalTask]
	now        func() time.Time
}

func localTerminal(state string) bool {
	switch state {
	case "succeeded", "failed", "cancelled", "reconciling":
		return true
	}
	return false
}

func NewLocalTasks(stateDir string) (*LocalTasks, error) {
	if stateDir == "" {
		return nil, protectionError("request_invalid", "本机任务缺少状态目录")
	}
	l := &LocalTasks{dir: filepath.Join(stateDir, "local-tasks"), cancels: map[string]func(string){}, now: time.Now}
	l.hub.init(func(t LocalTask) string { return t.TaskRef.TaskID }, func(t LocalTask) bool { return localTerminal(t.State) })
	err := l.withStore(func() error {
		path := filepath.Join(l.dir, ".executor.json")
		var r struct {
			ExecutorID string `json:"executor_id"`
		}
		b, err := os.ReadFile(path)
		if err == nil {
			if json.Unmarshal(b, &r) != nil || !protection.ValidID(r.ExecutorID) {
				return protectionError("record_corrupt", "本机任务执行环境身份损坏")
			}
			l.executorID = r.ExecutorID
			return nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return protectionError("record_corrupt", "无法读取本机任务执行环境身份")
		}
		if r.ExecutorID, err = identity.NewID(); err != nil {
			return err
		}
		l.executorID = r.ExecutorID
		return protectionAtomic(path, r)
	})
	return l, err
}

func (l *LocalTasks) withStore(fn func() error) error {
	l.storeMu.Lock()
	defer l.storeMu.Unlock()
	if err := os.MkdirAll(l.dir, 0700); err != nil {
		return protectionError("record_corrupt", "无法创建本机任务目录")
	}
	st, err := os.Lstat(l.dir)
	if err != nil || !st.IsDir() {
		return protectionError("record_corrupt", "本机任务目录无效")
	}
	if err = os.Chmod(l.dir, 0700); err != nil {
		return err
	}
	unlock, err := protectionStoreLock(filepath.Join(l.dir, ".local.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	return fn()
}

func (l *LocalTasks) path(id string) string { return filepath.Join(l.dir, id+".json") }
func (l *LocalTasks) lockPath(id string) string {
	return filepath.Join(l.dir, id+".lock")
}

func (l *LocalTasks) load(id string) (LocalTask, error) {
	var t LocalTask
	if !protection.ValidID(id) {
		return t, protectionError("record_corrupt", "本机任务标识无效")
	}
	st, err := os.Lstat(l.path(id))
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 || st.Size() > 1<<20 {
		return t, protectionError("record_corrupt", "本机任务记录缺失或不可读")
	}
	b, err := os.ReadFile(l.path(id))
	if err != nil || json.Unmarshal(b, &t) != nil || t.Schema != localTaskSchema || t.TaskRef.TaskID != id || t.TaskRef.ExecutorID != l.executorID || t.Kind == "" || t.Revision == 0 || t.SubmissionKeyDigest == "" || t.RequestDigest == "" || t.CreatedAt.IsZero() {
		return LocalTask{}, protectionError("record_corrupt", "本机任务记录格式无效")
	}
	switch t.State {
	case "running", "cancelling", "succeeded", "failed", "cancelled", "reconciling":
	default:
		return LocalTask{}, protectionError("record_corrupt", "本机任务状态无效")
	}
	return t, nil
}

// view reports a running record whose process is gone as interrupted. The
// job is not rerun; the user starts a new task when wanted.
func (l *LocalTasks) view(t LocalTask) (LocalTask, error) {
	if localTerminal(t.State) || l.alive(t.TaskRef.TaskID) {
		return t, nil
	}
	t.State = "reconciling"
	t.Stage = ""
	t.Error = &ProtectionTaskError{Code: "interrupted", Reason: "客户端进程在任务完成前退出，结果未核实", Suggestion: "需要时重新发起；仓库只追加，已复制的内容会在下次同步时校验", Retry: "replan"}
	t.Revision++
	t.ObservedAt = l.now().UTC()
	return t, protectionAtomic(l.path(t.TaskRef.TaskID), t)
}

// alive reports whether some process holds the task's execution lock.
func (l *LocalTasks) alive(id string) bool {
	unlock, err := protectionStoreLock(l.lockPath(id))
	if err != nil {
		return true
	}
	unlock()
	return false
}

func (l *LocalTasks) loadAll() ([]LocalTask, error) {
	es, err := os.ReadDir(l.dir)
	if err != nil {
		return nil, err
	}
	out := []LocalTask{}
	for _, e := range es {
		if strings.HasPrefix(e.Name(), ".") || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		t, err := l.load(strings.TrimSuffix(e.Name(), ".json"))
		if err != nil {
			return nil, err
		}
		if t, err = l.view(t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

// Start registers and runs job. The same submission key returns its task,
// also after a restart, and never runs the job again.
func (l *LocalTasks) Start(kind string, subject LocalTaskSubject, submissionKey string, job LocalJob) (LocalTask, error) {
	if kind == "" || job == nil || submissionKey == "" || len(submissionKey) > 256 {
		return LocalTask{}, protectionError("request_invalid", "本机任务类型或提交标识无效")
	}
	if l.hub.isClosing() {
		return LocalTask{}, protectionError("resource_busy", "Valise 正在关闭，不再开始新的本机任务")
	}
	digest, _ := protection.Digest(struct {
		Kind    string
		Subject LocalTaskSubject
	}{kind, subject})
	keyDigest, _ := protection.Digest(submissionKey)
	var t LocalTask
	var release func()
	err := l.withStore(func() error {
		tasks, err := l.loadAll()
		if err != nil {
			return err
		}
		for _, existing := range tasks {
			if existing.SubmissionKeyDigest == keyDigest {
				if existing.RequestDigest != digest {
					return protectionError("request_conflict", "同一提交标识已经用于另一项本机任务")
				}
				t = existing
				return nil
			}
		}
		id, err := identity.NewID()
		if err != nil {
			return err
		}
		if release, err = protectionStoreLock(l.lockPath(id)); err != nil {
			return err
		}
		now := l.now().UTC()
		t = LocalTask{Schema: localTaskSchema, TaskRef: ProtectionTaskRef{ExecutorID: l.executorID, TaskID: id}, Kind: kind, Subject: subject, RequestDigest: digest, SubmissionKeyDigest: keyDigest, State: "running", Revision: 1, CreatedAt: now, ObservedAt: now}
		if err = protectionAtomic(l.path(id), t); err != nil {
			release()
			release = nil
		}
		return err
	})
	if err != nil || release == nil {
		return cloneProtection(t), err
	}
	ctx, cancel := context.WithCancel(l.hub.ctx)
	reason := ""
	l.mu.Lock()
	l.cancels[t.TaskRef.TaskID] = func(why string) {
		l.mu.Lock()
		if reason == "" {
			reason = why
		}
		l.mu.Unlock()
		cancel()
	}
	l.mu.Unlock()
	started := l.hub.start(t.TaskRef.TaskID, func(stop <-chan struct{}) {
		defer release()
		defer cancel()
		go func() {
			select {
			case <-stop:
				l.cancelWith(t.TaskRef.TaskID, "client_exit")
			case <-ctx.Done():
			}
		}()
		res, jobErr := job(ctx, func(stage string) {
			_, _ = l.update(t.TaskRef.TaskID, func(t *LocalTask) bool {
				if t.State != "running" && t.State != "cancelling" {
					return false
				}
				t.Stage = stage
				return true
			})
		})
		l.mu.Lock()
		why := reason
		delete(l.cancels, t.TaskRef.TaskID)
		l.mu.Unlock()
		_, _ = l.update(t.TaskRef.TaskID, func(t *LocalTask) bool {
			t.Stage = ""
			if res != nil {
				t.Result, _ = json.Marshal(res)
			}
			switch {
			case jobErr == nil:
				t.State = "succeeded"
				t.Error = nil
			case why != "":
				// Stopped at an interruptible boundary; what was already
				// written stays and is reported, not rolled back.
				t.State = "cancelled"
				t.Error = &ProtectionTaskError{Code: "cancelled", Reason: "任务已停止，已完成的部分保留", Suggestion: "需要时重新发起", Retry: "replan"}
				if why == "client_exit" {
					t.Error = &ProtectionTaskError{Code: "client_exit", Reason: "Valise 关闭时停止了这项本机任务，已完成的部分保留", Suggestion: "重新打开 Valise 后再次发起", Retry: "replan"}
				}
			default:
				t.State = "failed"
				t.Error = localTaskError(jobErr)
			}
			return true
		})
	})
	if !started {
		cancel()
		release()
		return l.update(t.TaskRef.TaskID, func(t *LocalTask) bool {
			t.State = "cancelled"
			t.Error = &ProtectionTaskError{Code: "client_exit", Reason: "Valise 正在关闭，任务未开始", Suggestion: "重新打开 Valise 后再次发起", Retry: "replan"}
			return true
		})
	}
	return cloneProtection(t), nil
}

// localTaskError keeps the reason and suggestion only; raw details such as
// command output are never written to the index.
func localTaskError(err error) *ProtectionTaskError {
	if ue, ok := valise.As(err); ok {
		return &ProtectionTaskError{Code: ue.Code, Reason: ue.Reason, Suggestion: ue.Suggestion, Retry: "replan"}
	}
	return &ProtectionTaskError{Code: "task_failed", Reason: "本机任务未完成", Suggestion: "检查连接和磁盘后重新发起", Retry: "replan"}
}

func (l *LocalTasks) update(id string, fn func(*LocalTask) bool) (LocalTask, error) {
	var t LocalTask
	changed := false
	err := l.withStore(func() error {
		var err error
		if t, err = l.load(id); err != nil {
			return err
		}
		if !fn(&t) {
			return nil
		}
		changed = true
		t.Revision++
		t.ObservedAt = l.now().UTC()
		return protectionAtomic(l.path(id), t)
	})
	if err == nil && changed {
		l.hub.notify(cloneProtection(t))
	}
	return cloneProtection(t), err
}

func (l *LocalTasks) Get(ref ProtectionTaskRef) (LocalTask, error) {
	if ref.ExecutorID != l.executorID || !protection.ValidID(ref.TaskID) {
		return LocalTask{}, protectionError("request_invalid", "本机任务引用不属于当前执行环境")
	}
	var t LocalTask
	err := l.withStore(func() error {
		var err error
		if t, err = l.load(ref.TaskID); err != nil {
			return err
		}
		t, err = l.view(t)
		return err
	})
	return cloneProtection(t), err
}

// List returns all local tasks, newest first.
func (l *LocalTasks) List() ([]LocalTask, error) {
	var tasks []LocalTask
	err := l.withStore(func() error { var e error; tasks, e = l.loadAll(); return e })
	sort.Slice(tasks, func(i, j int) bool {
		if !tasks[i].CreatedAt.Equal(tasks[j].CreatedAt) {
			return tasks[i].CreatedAt.After(tasks[j].CreatedAt)
		}
		return tasks[i].TaskRef.TaskID < tasks[j].TaskRef.TaskID
	})
	return cloneProtection(tasks), err
}

// Watch delivers the current view and later changes; a terminal view closes
// the channel. Unsubscribing never cancels the task.
func (l *LocalTasks) Watch(ref ProtectionTaskRef) (<-chan LocalTask, func(), error) {
	t, err := l.Get(ref)
	if err != nil {
		return nil, nil, err
	}
	ch, unsubscribe := l.hub.watch(t)
	return ch, unsubscribe, nil
}

func (l *LocalTasks) cancelWith(id, reason string) bool {
	l.mu.Lock()
	cancel := l.cancels[id]
	l.mu.Unlock()
	if cancel == nil {
		return false
	}
	cancel(reason)
	return true
}

// Cancel stops a task of this process at its next interruptible boundary.
// The view reads cancelling until the job has returned.
func (l *LocalTasks) Cancel(ref ProtectionTaskRef) (LocalTask, error) {
	t, err := l.Get(ref)
	if err != nil || localTerminal(t.State) {
		return t, err
	}
	l.mu.Lock()
	_, ours := l.cancels[ref.TaskID]
	l.mu.Unlock()
	if !ours {
		return t, valise.New("resource_busy", "这项本机任务由另一个 Valise 进程执行", "在发起它的窗口里取消")
	}
	t, err = l.update(ref.TaskID, func(t *LocalTask) bool {
		if t.State != "running" {
			return false
		}
		t.State = "cancelling"
		t.CancelRequested = true
		return true
	})
	if err != nil {
		return t, err
	}
	l.cancelWith(ref.TaskID, "cancelled")
	return t, nil
}

// Shutdown is for a normal process exit: it stops new tasks, asks running
// jobs to stop at their next boundary and waits for them until ctx ends.
func (l *LocalTasks) Shutdown(ctx context.Context) error {
	return l.hub.shutdown(ctx)
}
