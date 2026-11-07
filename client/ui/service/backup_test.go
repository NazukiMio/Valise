package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"valise/core/identity"
	"valise/core/protection"
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
