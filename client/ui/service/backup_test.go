package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"valise/core/identity"
	"valise/core/protection"
	"valise/core/valise"
)

// Typed RPC substitute: these tests execute no SSH or harness commands.
type backupBoundary struct {
	mu          sync.Mutex
	plan        protection.ServerPlan
	mode        string
	calls       int
	statusCalls int
	callError   error
	statusError error
	run         protection.BackupRun
	hook        func(protection.BackupParams)
}

func newBackupBoundary() *backupBoundary {
	d := protection.NewDefault()
	d.Schedule.DailyAt = "03:00"
	d.Schedule.Timezone = "UTC"
	d.Period = protection.Period{Unit: "month", Timezone: "UTC"}
	return &backupBoundary{mode: "accepted", plan: protection.ServerPlan{SchemaVersion: protection.Schema, AgentRef: identity.AgentRef{ServerUID: "server", InstallID: "demo", InstanceID: "demo", Generation: "generation"}, Revision: 1, Desired: d, AcceptedAt: time.Now().UTC()}}
}
func (b *backupBoundary) rpc(_ context.Context, _ string, method string, p, out any) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch method {
	case protection.MethodGet:
		*out.(*protection.View) = protection.View{Mode: b.mode, ServerPlan: ptrBackupPlan(cloneProtection(b.plan))}
	case protection.MethodBackup:
		b.calls++
		req := p.(protection.BackupParams)
		if b.hook != nil {
			b.hook(req)
		}
		digest, _ := protection.Digest(req)
		b.run = protection.BackupRun{Schema: protection.BackupRunSchema, RunID: req.RunID, AgentRef: req.AgentRef, Request: req, RequestDigest: digest, Plan: cloneProtection(b.plan), State: "succeeded", Snapshot: &identity.SnapshotRef{VaultID: "vault", SnapshotID: "snapshot"}, AcceptedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
		*out.(*protection.BackupRun) = cloneProtection(b.run)
		return b.callError
	case protection.MethodRunStatus:
		b.statusCalls++
		req := p.(protection.BackupStatusParams)
		if req.RunID != b.run.RunID || req.AgentRef != b.run.AgentRef {
			return errors.New("missing")
		}
		*out.(*protection.BackupRun) = cloneProtection(b.run)
		return b.statusError
	default:
		panic(method)
	}
	return nil
}
func ptrBackupPlan(p protection.ServerPlan) *protection.ServerPlan { return &p }
func makeBackupController(t *testing.T, b *backupBoundary, review BackupReview) *BackupController {
	t.Helper()
	c, err := NewBackupController(BackupControllerConfig{StateDir: t.TempDir(), RPC: b.rpc, Review: review})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func prepareBackup(t *testing.T, c *BackupController, b *backupBoundary) BackupPlan {
	t.Helper()
	p, err := c.PrepareBackup(context.Background(), "host", b.plan.AgentRef)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func approveBackup(context.Context, string, BackupPlan) (bool, error) { return true, nil }
func TestBackupWaitingThenReviewAndDurableDedup(t *testing.T) {
	b := newBackupBoundary()
	c := makeBackupController(t, b, nil)
	p := prepareBackup(t, c, b)
	if p.ServerPlan.Scope.Tier != "full" || !p.IncludesCredentials {
		t.Fatal("default full/credentials not disclosed")
	}
	tsk, err := c.StartBackup(context.Background(), p.PlanID, "key")
	if err != nil || tsk.State != "waiting" || b.calls != 0 {
		t.Fatalf("waiting %+v %v", tsk, err)
	}
	c.config.Review = func(_ context.Context, _ string, review BackupPlan) (bool, error) {
		if review.ServerPlan.Revision != 1 || !review.IncludesCredentials {
			t.Fatal("wrong review")
		}
		return true, nil
	}
	b.hook = func(req protection.BackupParams) {
		durable, err := c.load(tsk.TaskRef.TaskID)
		if err != nil || durable.State != "running" || durable.RunID != req.RunID {
			t.Fatal("RPC before durable task")
		}
	}
	tsk, err = c.StartBackup(context.Background(), p.PlanID, "key")
	if err != nil || tsk.State != "succeeded" || tsk.Run == nil {
		t.Fatalf("start %+v %v", tsk, err)
	}
	again, err := c.StartBackup(context.Background(), p.PlanID, "key")
	if err != nil || again.TaskRef != tsk.TaskRef || b.calls != 1 {
		t.Fatalf("dedup %+v %v", again, err)
	}
	// Reopening the private index restores observations, never old confirmation.
	reopened, err := NewBackupController(BackupControllerConfig{StateDir: c.config.StateDir, RPC: b.rpc, Review: approveBackup})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = reopened.StartBackup(context.Background(), p.PlanID, "key"); err == nil {
		t.Fatal("reloaded confirmation")
	}
	fresh := prepareBackup(t, reopened, b)
	again, err = reopened.StartBackup(context.Background(), fresh.PlanID, "key")
	if err != nil || again.TaskRef != tsk.TaskRef || b.calls != 1 {
		t.Fatalf("restart dedup %+v %v", again, err)
	}
	for _, path := range []string{c.dir(), filepath.Join(c.dir(), tsk.TaskRef.TaskID+".json")} {
		st, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		want := os.FileMode(0600)
		if st.IsDir() {
			want = 0700
		}
		if st.Mode().Perm() != want {
			t.Fatalf("permissions %v", st.Mode())
		}
	}
}
func TestBackupTimeoutOnlyReconcilesByStatus(t *testing.T) {
	b := newBackupBoundary()
	b.callError = errors.New("stderr SECRET-TOKEN")
	c := makeBackupController(t, b, approveBackup)
	p := prepareBackup(t, c, b)
	tsk, err := c.StartBackup(context.Background(), p.PlanID, "key")
	if err != nil || tsk.State != "reconciling" {
		t.Fatalf("timeout %+v %v", tsk, err)
	}
	for range 2 {
		again, err := c.StartBackup(context.Background(), p.PlanID, "key")
		if err != nil || again.State != "reconciling" || b.calls != 1 {
			t.Fatalf("resent %+v %v", again, err)
		}
	}
	raw, _ := os.ReadFile(filepath.Join(c.dir(), tsk.TaskRef.TaskID+".json"))
	if strings.Contains(string(raw), "SECRET-TOKEN") {
		t.Fatal("raw error persisted")
	}
	reopened, err := NewBackupController(BackupControllerConfig{StateDir: c.config.StateDir, RPC: b.rpc})
	if err != nil {
		t.Fatal(err)
	}
	b.statusError = errors.New("offline")
	tsk, err = reopened.GetBackupTask(context.Background(), tsk.TaskRef)
	if err != nil || tsk.State != "reconciling" || b.calls != 1 {
		t.Fatalf("offline %+v %v", tsk, err)
	}
	b.statusError = nil
	tsk, err = reopened.GetBackupTask(context.Background(), tsk.TaskRef)
	if err != nil || tsk.State != "succeeded" || tsk.Run.Snapshot == nil || b.calls != 1 || b.statusCalls != 2 {
		t.Fatalf("reconcile %+v %v", tsk, err)
	}
}
func TestBackupReviewRechecksFactsAndClonesDTO(t *testing.T) {
	b := newBackupBoundary()
	c := makeBackupController(t, b, func(_ context.Context, _ string, p BackupPlan) (bool, error) {
		p.ServerPlan.Schedule.Enabled = new(true)
		b.plan.Revision++
		return true, nil
	})
	p := prepareBackup(t, c, b)
	p.ServerPlan.Scope.Tier = "soul"
	p.IncludesCredentials = false
	if _, err := c.StartBackup(context.Background(), p.PlanID, "key"); err == nil || b.calls != 0 {
		t.Fatal("stale plan executed")
	}
}
func TestBackupConflictExpiryAndSinglePlanConsumption(t *testing.T) {
	b := newBackupBoundary()
	c := makeBackupController(t, b, approveBackup)
	p := prepareBackup(t, c, b)
	first, err := c.StartBackup(context.Background(), p.PlanID, "key")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.StartBackup(context.Background(), p.PlanID, "new-key"); err == nil {
		t.Fatal("plan used twice")
	}
	b.plan.Revision++
	other := prepareBackup(t, c, b)
	if _, err = c.StartBackup(context.Background(), other.PlanID, "key"); err == nil {
		t.Fatal("key conflict accepted")
	}
	c.now = func() time.Time { return other.ExpiresAt }
	if _, err = c.StartBackup(context.Background(), other.PlanID, "expired"); err == nil {
		t.Fatal("expired plan accepted")
	}
	if b.calls != 1 || first.State != "succeeded" {
		t.Fatal("unexpected calls")
	}
}
func TestBackupLegacyAndUnboundAreRejected(t *testing.T) {
	b := newBackupBoundary()
	c := makeBackupController(t, b, approveBackup)
	b.mode = "legacy"
	if _, err := c.PrepareBackup(context.Background(), "host", b.plan.AgentRef); err == nil {
		t.Fatal("legacy acquired default full")
	}
	b.mode = "accepted"
	ref := b.plan.AgentRef
	ref.Generation = "other"
	if _, err := c.PrepareBackup(context.Background(), "host", ref); err == nil {
		t.Fatal("wrong identity accepted")
	}
}
func TestBackupPartialKeepsSnapshotAndWrongStatusIsUnknown(t *testing.T) {
	b := newBackupBoundary()
	b.callError = errors.New("timeout")
	c := makeBackupController(t, b, approveBackup)
	p := prepareBackup(t, c, b)
	tsk, err := c.StartBackup(context.Background(), p.PlanID, "key")
	if err != nil {
		t.Fatal(err)
	}
	b.run.AgentRef.Generation = "wrong"
	tsk, err = c.GetBackupTask(context.Background(), tsk.TaskRef)
	if err != nil || tsk.State != "reconciling" {
		t.Fatal("foreign result accepted")
	}
	b.run.AgentRef = b.plan.AgentRef
	b.run.State = "partial"
	b.run.Components = []protection.BackupComponent{{Kind: "server_backup", State: "succeeded"}, {Kind: "server_copy", State: "failed", ErrorCode: "copy_failed"}}
	tsk, err = c.GetBackupTask(context.Background(), tsk.TaskRef)
	if err != nil || tsk.State != "partial" || tsk.Run.Snapshot.VaultID != "vault" || len(tsk.Run.Components) != 2 || b.calls != 1 {
		t.Fatalf("partial %+v %v", tsk, err)
	}
}
func TestBackupIndexCorruptCannotExecute(t *testing.T) {
	b := newBackupBoundary()
	c := makeBackupController(t, b, approveBackup)
	p := prepareBackup(t, c, b)
	path := filepath.Join(c.dir(), strings.Repeat("a", 32)+".json")
	if err := os.WriteFile(path, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.StartBackup(context.Background(), p.PlanID, "key"); err == nil || b.calls != 0 {
		t.Fatal("corrupt index ignored")
	}
}
func TestBackupConcurrentSubmissionsExecuteOnce(t *testing.T) {
	b := newBackupBoundary()
	c := makeBackupController(t, b, approveBackup)
	p := prepareBackup(t, c, b)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = c.StartBackup(context.Background(), p.PlanID, "same") }()
	}
	wg.Wait()
	if b.calls != 1 {
		t.Fatalf("calls %d", b.calls)
	}
}

// asyncBoundary is a typed backup-runs/2 substitute. Its run advances only
// when the test says so; it executes no SSH or harness commands.
type asyncBoundary struct {
	*backupBoundary
	caps      []string
	execution string
	gate      chan struct{}
	cancels   int
	status    func(*protection.BackupRun)
}

func newAsyncBoundary(execution string) *asyncBoundary {
	return &asyncBoundary{backupBoundary: newBackupBoundary(), caps: []string{protection.BackupCapability, protection.BackupCapabilityV2}, execution: execution}
}
func (a *asyncBoundary) capabilities(context.Context, string) ([]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.caps), nil
}
func (a *asyncBoundary) rpc(ctx context.Context, host, method string, p, out any) error {
	switch method {
	case protection.MethodGet:
		return a.backupBoundary.rpc(ctx, host, method, p, out)
	case protection.MethodBackup:
		req := p.(protection.BackupParams)
		digest, _ := protection.Digest(req)
		a.mu.Lock()
		a.calls++
		if req.Detach != slices.Contains(a.caps, protection.BackupCapabilityV2) {
			a.mu.Unlock()
			return valise.New("request_invalid", "detach sent without backup-runs/2", "fix the client")
		}
		now := time.Now().UTC()
		a.run = protection.BackupRun{Schema: protection.BackupRunSchema, RunID: req.RunID, AgentRef: req.AgentRef, Request: req, RequestDigest: digest, Plan: cloneProtection(a.plan), State: "accepted", Execution: a.execution, Stage: protection.StageQueued, AcceptedAt: now, UpdatedAt: now}
		gate := a.gate
		a.mu.Unlock()
		if a.execution == protection.ExecutionAttached && gate != nil {
			a.mu.Lock()
			a.run.State = "running"
			a.run.Stage = protection.StageSealing
			a.mu.Unlock()
			select {
			case <-gate:
			case <-ctx.Done():
				return ctx.Err()
			}
			a.mu.Lock()
			a.run.State = "succeeded"
			a.run.Stage = ""
			a.run.Snapshot = &identity.SnapshotRef{VaultID: "vault", SnapshotID: "snapshot"}
			a.mu.Unlock()
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		*out.(*protection.BackupRun) = cloneProtection(a.run)
		return nil
	case protection.MethodRunStatus:
		a.mu.Lock()
		defer a.mu.Unlock()
		a.statusCalls++
		req := p.(protection.BackupStatusParams)
		if req.RunID != a.run.RunID {
			return valise.New("run_not_found", "missing", "retry")
		}
		if a.status != nil {
			a.status(&a.run)
		}
		*out.(*protection.BackupRun) = cloneProtection(a.run)
		return nil
	case protection.MethodRunCancel:
		a.mu.Lock()
		defer a.mu.Unlock()
		a.cancels++
		at := time.Now().UTC()
		a.run.CancelRequestedAt = &at
		*out.(*protection.BackupRun) = cloneProtection(a.run)
		return nil
	}
	panic(method)
}
func (a *asyncBoundary) set(fn func(*protection.BackupRun)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	fn(&a.run)
}
func (a *asyncBoundary) counts() (int, int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls, a.cancels
}
func makeAsyncController(t *testing.T, a *asyncBoundary, dir string) *BackupController {
	t.Helper()
	if dir == "" {
		dir = t.TempDir()
	}
	c, err := NewBackupController(BackupControllerConfig{StateDir: dir, RPC: a.rpc, Review: approveBackup, Capabilities: a.capabilities, PollInterval: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })
	return c
}
func waitBackupState(t *testing.T, ch <-chan BackupTask, states ...string) BackupTask {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case v, ok := <-ch:
			if !ok {
				t.Fatalf("closed before %v", states)
			}
			if slices.Contains(states, v.State) {
				return v
			}
		case <-deadline:
			t.Fatalf("timeout waiting for %v", states)
		}
	}
}

func TestBackupAsyncStartReturnsBeforeAttachedRunCompletes(t *testing.T) {
	a := newAsyncBoundary(protection.ExecutionAttached)
	a.gate = make(chan struct{})
	c := makeAsyncController(t, a, "")
	p := prepareBackup(t, c, a.backupBoundary)
	tsk, err := c.StartBackup(context.Background(), p.PlanID, "key")
	if err != nil || tsk.State != "running" || !tsk.Async || tsk.Run != nil {
		t.Fatalf("start %+v %v", tsk, err)
	}
	ch, unsubscribe, err := c.WatchBackupTask(context.Background(), tsk.TaskRef)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	// Status queries reveal the attached execution while the call is open.
	deadline := time.After(5 * time.Second)
	for seen := false; !seen; {
		select {
		case v := <-ch:
			seen = v.Execution == protection.ExecutionAttached && v.Run != nil && v.Run.Stage == protection.StageSealing
		case <-deadline:
			t.Fatal("attached execution not observed")
		}
	}
	list, err := c.ListBackupTasks(context.Background())
	if err != nil || len(list) != 1 || list[0].TaskRef != tsk.TaskRef || list[0].State != "running" {
		t.Fatalf("list %+v %v", list, err)
	}
	close(a.gate)
	done := waitBackupState(t, ch, "succeeded")
	if done.Run == nil || done.Run.Snapshot == nil {
		t.Fatalf("done %+v", done)
	}
	if _, ok := <-ch; ok {
		t.Fatal("terminal view did not close the subscription")
	}
	if calls, _ := a.counts(); calls != 1 {
		t.Fatalf("calls %d", calls)
	}
	// Same key: the same task, no new call.
	again, err := c.StartBackup(context.Background(), p.PlanID, "key")
	if err != nil || again.TaskRef != tsk.TaskRef || again.State != "succeeded" {
		t.Fatalf("dedup %+v %v", again, err)
	}
}

func TestBackupAsyncDetachedSurvivesRestartAndUnsubscribe(t *testing.T) {
	a := newAsyncBoundary(protection.ExecutionDetached)
	c := makeAsyncController(t, a, "")
	p := prepareBackup(t, c, a.backupBoundary)
	tsk, err := c.StartBackup(context.Background(), p.PlanID, "key")
	if err != nil || tsk.State != "running" {
		t.Fatalf("start %+v %v", tsk, err)
	}
	ch, unsubscribe, err := c.WatchBackupTask(context.Background(), tsk.TaskRef)
	if err != nil {
		t.Fatal(err)
	}
	v := waitBackupState(t, ch, "running")
	unsubscribe()
	if _, ok := <-ch; ok {
		// drain the pending view; the channel must close after unsubscribe
		if _, ok := <-ch; ok {
			t.Fatal("unsubscribe kept the channel open")
		}
	}
	_ = v
	// Close this process while the server still runs the unit.
	if err := c.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.StartBackup(context.Background(), p.PlanID, "other"); err == nil {
		t.Fatal("started after shutdown")
	}
	durable, err := c.load(tsk.TaskRef.TaskID)
	if err != nil || durable.State != "running" || durable.Execution != protection.ExecutionDetached {
		t.Fatalf("durable %+v %v", durable, err)
	}
	a.set(func(r *protection.BackupRun) {
		r.State = "succeeded"
		r.Snapshot = &identity.SnapshotRef{VaultID: "vault", SnapshotID: "snapshot"}
	})
	reopened := makeAsyncController(t, a, c.config.StateDir)
	ch, unsubscribe, err = reopened.WatchBackupTask(context.Background(), tsk.TaskRef)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	if done := waitBackupState(t, ch, "succeeded"); done.Run.Snapshot == nil {
		t.Fatalf("restart %+v", done)
	}
	if calls, _ := a.counts(); calls != 1 {
		t.Fatalf("restart replayed: %d", calls)
	}
}

func TestBackupAsyncCancelReadsCancellingUntilConfirmed(t *testing.T) {
	a := newAsyncBoundary(protection.ExecutionDetached)
	c := makeAsyncController(t, a, "")
	p := prepareBackup(t, c, a.backupBoundary)
	a.status = func(r *protection.BackupRun) {
		if r.State == "accepted" {
			r.State = "running"
			r.Stage = protection.StageSealing
		}
	}
	tsk, err := c.StartBackup(context.Background(), p.PlanID, "key")
	if err != nil {
		t.Fatal(err)
	}
	ch, unsubscribe, err := c.WatchBackupTask(context.Background(), tsk.TaskRef)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	for v := waitBackupState(t, ch, "running"); v.Run == nil || v.Run.State != "running"; v = waitBackupState(t, ch, "running") {
	}
	got, err := c.CancelBackupTask(context.Background(), tsk.TaskRef)
	if err != nil || got.State != "cancelling" || !got.CancelRequested {
		t.Fatalf("cancel %+v %v", got, err)
	}
	if _, cancels := a.counts(); cancels != 1 {
		t.Fatalf("cancels %d", cancels)
	}
	// The executor stops at the copy safe point and keeps the snapshot.
	a.set(func(r *protection.BackupRun) {
		r.State = "cancelled"
		r.ErrorCode = "cancelled"
		r.Stage = ""
		r.Snapshot = &identity.SnapshotRef{VaultID: "vault", SnapshotID: "snapshot"}
	})
	done := waitBackupState(t, ch, "cancelled")
	if done.Run == nil || done.Run.Snapshot == nil {
		t.Fatalf("cancelled %+v", done)
	}
	reloaded, err := c.load(tsk.TaskRef.TaskID)
	if err != nil || reloaded.State != "cancelled" {
		t.Fatalf("durable %+v %v", reloaded, err)
	}
	if calls, _ := a.counts(); calls != 1 {
		t.Fatal("cancel replayed the backup")
	}
}

func TestBackupOldPorterStaysSynchronous(t *testing.T) {
	a := newAsyncBoundary(protection.ExecutionAttached)
	a.caps = []string{protection.BackupCapability}
	c := makeAsyncController(t, a, "")
	p := prepareBackup(t, c, a.backupBoundary)
	tsk, err := c.StartBackup(context.Background(), p.PlanID, "key")
	// Without a gate the attached substitute answers accepted at once, so a
	// synchronous Start shows the call's own result and is not followed.
	if err != nil || tsk.Async || tsk.State != "running" || tsk.Run == nil {
		t.Fatalf("sync %+v %v", tsk, err)
	}
	if _, err := c.CancelBackupTask(context.Background(), tsk.TaskRef); err == nil {
		t.Fatal("old porter accepted cancel")
	}
	if _, cancels := a.counts(); cancels != 0 {
		t.Fatal("cancel sent to old porter")
	}
	a.caps = nil
	b := newBackupBoundary()
	sync := makeBackupController(t, b, approveBackup)
	p = prepareBackup(t, sync, b)
	if done, err := sync.StartBackup(context.Background(), p.PlanID, "k"); err != nil || done.State != "succeeded" || done.Async {
		t.Fatalf("no capabilities %+v %v", done, err)
	}
}

func TestBackupAsyncLostCallIsUnknownAndNotResent(t *testing.T) {
	a := newAsyncBoundary(protection.ExecutionDetached)
	c := makeAsyncController(t, a, "")
	p := prepareBackup(t, c, a.backupBoundary)
	failing := func(ctx context.Context, host, method string, params, out any) error {
		if method == protection.MethodBackup {
			a.mu.Lock()
			a.calls++
			a.mu.Unlock()
			return errors.New("connection reset SECRET")
		}
		return a.rpc(ctx, host, method, params, out)
	}
	c.config.RPC = failing
	tsk, err := c.StartBackup(context.Background(), p.PlanID, "key")
	if err != nil {
		t.Fatal(err)
	}
	ch, unsubscribe, err := c.WatchBackupTask(context.Background(), tsk.TaskRef)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	got := waitBackupState(t, ch, "reconciling")
	if got.Error == nil || got.Error.Code != "result_unknown" {
		t.Fatalf("unknown %+v", got)
	}
	if _, err := c.CancelBackupTask(context.Background(), tsk.TaskRef); err == nil {
		t.Fatal("cancelled an unknown result")
	}
	if calls, _ := a.counts(); calls != 1 {
		t.Fatal("resent")
	}
}

func TestBackupShutdownWaitsForAttachedCall(t *testing.T) {
	a := newAsyncBoundary(protection.ExecutionAttached)
	a.gate = make(chan struct{})
	c := makeAsyncController(t, a, "")
	p := prepareBackup(t, c, a.backupBoundary)
	tsk, err := c.StartBackup(context.Background(), p.PlanID, "key")
	if err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	go func() { time.Sleep(20 * time.Millisecond); close(a.gate) }()
	if err := c.Shutdown(short); err != nil {
		t.Fatalf("shutdown did not wait: %v", err)
	}
	done, err := c.load(tsk.TaskRef.TaskID)
	if err != nil || done.State != "succeeded" {
		t.Fatalf("after shutdown %+v %v", done, err)
	}
}
