package gui

import (
	"context"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"valise/core/identity"
	"valise/core/journey"
	"valise/core/revive"
	"valise/ui/service"
)

func healthRestoreWorld(t *testing.T) (*App, *ui.Tester) {
	t.Helper()
	a, tt := world(t)
	ref := identity.AgentRef{ServerUID: "home", InstallID: "xiaoman", InstanceID: "xiaoman", Generation: "current"}
	a.agents.value.Items[0].AgentRef = &ref
	for i := range a.timeline.value.Items {
		p := &a.timeline.value.Items[i]
		if p.RecordedInstanceID != "xiaoman" || i > 2 {
			continue
		}
		p.Copies[0].LocationRef = journey.LocationRef{Kind: "server_vault", ID: "home", AuthorityID: "home"}
		p.Copies[0].HeaderDigest = "selected-header"
		p.Copies[0].Metadata.InstanceID = "xiaoman"
		p.Copies[0].Metadata.SnapshotID = p.SnapshotRef.SnapshotID
	}
	a.health["home/xiaoman"] = &healthState{at: time.Now(), canTest: true, report: &service.HealthReport{Host: "home", Instance: "xiaoman", Items: []service.HealthItem{{ID: "restore_test", Title: "备份文件测试", Level: service.HealthWarn, Text: "还没有测试过"}}}}
	a.openAgent("home", "xiaoman")
	a.agentTab = tabHealth
	tt.Frame()
	tt.Advance(time.Second)
	return a, tt
}

func TestHealthRestoreTestConfirmationCancelAndCloseWaitsForCleanup(t *testing.T) {
	a, tt := healthRestoreWorld(t)
	called := false
	a.runRestoreTest = func(ctx context.Context, selected service.SelectedRestoreTest) (*service.RestoreTest, error) {
		called = true
		if selected.Target != "home" || selected.Instance != "xiaoman" || selected.Snapshot != a.timeline.value.Items[0].SnapshotRef || selected.Source.Copy.HeaderDigest != "selected-header" {
			t.Fatal("test scoped to another agent")
		}
		if ctx.Err() != context.Canceled {
			t.Fatal("stop or close did not cancel the test")
		}
		return &service.RestoreTest{State: "cancelled", Cleanup: "removed", Tier: "full"}, nil
	}
	click(t, tt, T("restoretest.open"))
	if !tt.HasText(T("restoretest.confirm_hint")) || called {
		t.Fatal("test began before confirmation")
	}
	shot(t, tt, "restore-test-confirm")
	click(t, tt, T("common.cancel"))
	if called {
		t.Fatal("cancelled confirmation ran test")
	}
	var workers []func()
	a.run = func(f func()) { workers = append(workers, f) }
	r := a.restoreTest
	a.startRestoreTest(r, false)
	r.progress = service.Progress{Stage: service.StageStage, Done: 1, Total: 10}
	tt.Frame()
	tt.Advance(time.Second)
	if !tt.HasText(T("restoretest.progress.stage")) {
		t.Fatal("missing stage progress")
	}
	shot(t, tt, "restore-test-progress")
	closed := false
	if a.RequestClose(func() { closed = true }) || closed {
		t.Fatal("closed before test cleanup completed")
	}
	workers[0]() // readHealth is queued separately; this view test executes no RPC.
	if !called || !closed || r.j.busy || r.result == nil || r.result.State != "cancelled" {
		t.Fatal("close did not wait for the original test worker")
	}
	tt.Frame()
	tt.Advance(time.Second)
	if !tt.HasText(T("restoretest.state.cancelled")) || tt.HasText(T("restoretest.state.passed")) {
		t.Fatalf("cancel claimed pass: %q", tt.Texts())
	}
}

func TestHealthRestoreTestAndActualRecoveryAreSeparate(t *testing.T) {
	a, tt := healthRestoreWorld(t)
	when := time.Date(2026, 11, 11, 9, 0, 0, 0, time.Local)
	a.restoreTest = &restoreTestFlow{server: "home", instance: "xiaoman", result: &service.RestoreTest{State: "passed", OK: true, LocalVerified: true, ServerVerified: true, Cleanup: "removed", Files: 3, Bytes: 2048, Tier: "full", Created: when}}
	tt.Frame()
	tt.Advance(time.Second)
	hasAll(t, tt.Texts(), T("restoretest.state.passed"), T("restoretest.actual_none"), T("restoretest.result_hint"))
	shot(t, tt, "restore-test-passed")
	a.restoreTasks = []service.RestoreTask{{TargetAgent: a.agents.value.Items[0].AgentRef, Target: "home", Instance: "xiaoman", State: "succeeded", SwapState: revive.StateRestored, Updated: when}, {Target: "cloud", Instance: "xiaoman", State: "succeeded", SwapState: revive.StateHealthy, Updated: when.Add(time.Hour)}}
	tt.Frame()
	tt.Advance(time.Second)
	if !tt.HasText(T("restoretest.actual_files", "time", when.Format("2006-01-02 15:04:05"))) || tt.HasText(T("restoretest.actual_healthy", "time", when.Add(time.Hour).Format("2006-01-02 15:04:05"))) {
		t.Fatalf("actual result crossed agents or claimed startup: %q", tt.Texts())
	}
	shot(t, tt, "restore-test-and-file-recovery")
	a.restoreTasks[0].TargetAgent = nil
	tt.Frame()
	if !tt.HasText(T("restoretest.actual_legacy")) {
		t.Fatal("unbound recovery history guessed current identity")
	}
	a.restoreTest.result.OK = false
	a.restoreTest.result.State, a.restoreTest.result.Cleanup = "needs_cleanup", "pending"
	tt.Frame()
	tt.Advance(time.Second)
	hasAll(t, tt.Texts(), T("restoretest.state.needs_cleanup"), T("restoretest.cleanup"))
	shot(t, tt, "restore-test-cleanup-pending")
}

func TestRestoreOptionalBackupIsOffByDefaultAndOnlyForCurrentInstance(t *testing.T) {
	a, _, r := restoreWorld(t)
	r.step = "plan"
	r.plan = &service.PreparedRestore{Revive: &service.RevivePlan{Plan: revive.Plan{Mode: revive.ModeRollback, InstanceID: "xiaoman", Body: revive.BodyPlan{Action: revive.BodyKeep}}}}
	tt := ui.NewTester(func(c *ui.Context) { a.restorePlan(c, r) }, 1000, 900)
	if r.backupCurrent || !tt.HasText(T("restore.backup_current")) {
		t.Fatal("backup defaults to forced or has no choice")
	}
	click(t, tt, T("restore.backup_current"))
	if !r.backupCurrent {
		t.Fatal("choice did not update")
	}
	shot(t, tt, "restore-backup-optional")
	r.mode = revive.ModeClone
	tt.Frame()
	if tt.HasText(T("restore.backup_current")) {
		t.Fatal("clone offered current restore backup option")
	}
}
