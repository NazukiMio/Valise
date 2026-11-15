package gui

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"valise/core/environment"
	"valise/ui/service"
)

func TestParallelOldCleanupDisplaysExactScopeAndRequiresSeparateDeletionChoice(t *testing.T) {
	a, _, r := restoreWorld(t)
	r.step, r.parallelRead = "parallel_old_cleanup", true
	available := true
	r.parallelSwitchCapability = &available
	r.task = service.RestoreTask{Kind: service.RestoreKindEnvironment, Target: "home", Instance: "xiaoman", State: "succeeded", RuntimeRequested: true}
	r.oldCleanupPlan = &service.PreparedOldCleanup{Expires: time.Now().Add(time.Minute), View: environment.OldCleanupReview{
		PreparationParams: environment.PreparationParams{OperationID: strings.Repeat("a", 32)}, Digest: strings.Repeat("d", 64), Target: environment.Ref{Slot: environment.A, Revision: 1}, Current: environment.Ref{Slot: environment.B, Revision: 1}, State: "ready",
		Items: []environment.OldCleanupItem{{Kind: "data", Root: "state", Present: true, Files: 20, Bytes: 2048}, {Kind: "data", Root: "workspace", Present: true, Files: 10, Bytes: 1024}, {Kind: "program", Present: true, Files: 30, Bytes: 4096}, {Kind: "preparation", Present: true, Files: 40, Bytes: 8192}},
	}}
	for _, dimension := range []struct {
		w, h int
		name string
	}{{860, 560, "old-cleanup-small"}, {1180, 900, "old-cleanup-large"}} {
		tt := ui.NewTester(a.View, dimension.w, dimension.h)
		tt.Frame()
		tt.Advance(time.Second)
		for _, text := range []string{T("environment.old_cleanup_title"), T("environment.old_cleanup_effect"), T("environment.old_cleanup_data", "root", "workspace"), T("environment.old_cleanup_program"), T("environment.old_cleanup_confirm"), T("environment.old_cleanup_execute")} {
			if !tt.HasText(text) {
				t.Fatal("missing cleanup scope or independent choice", text, tt.Texts())
			}
		}
		shot(t, tt, dimension.name)
		if dimension.h == 560 {
			tt.Scroll(600, 450, 0, 320)
			tt.Advance(time.Second)
			shot(t, tt, dimension.name+"-scrolled")
		}
		click(t, tt, T("environment.old_cleanup_execute"))
		if r.j.busy || r.oldCleanupConfirm {
			t.Fatal("cleanup borrowed restoration or dialogue consent")
		}
	}
	tt := ui.NewTester(a.View, 1180, 900)
	tt.Frame()
	tt.Advance(time.Second)
	click(t, tt, T("environment.old_cleanup_confirm"))
	if !r.oldCleanupConfirm || r.j.busy {
		t.Fatal("deletion choice itself executed cleanup")
	}
	r.oldCleanupPlan.Expires = time.Now().Add(-time.Second)
	tt.Frame()
	tt.Advance(time.Second)
	if slices.Contains(tt.Texts(), T("environment.old_cleanup_execute")) || !tt.HasText(T("restore.expired")) {
		t.Fatal("expired cleanup review retained execution", tt.Texts())
	}
}

func TestParallelOldCleanupPendingRemainsAcceptedAndReachableAfterRestart(t *testing.T) {
	a, _, r := restoreWorld(t)
	r.step, r.parallelRead = "parallel_preparation", true
	available := true
	r.parallelSwitchCapability = &available
	r.task = service.RestoreTask{Kind: service.RestoreKindEnvironment, Target: "home", Instance: "xiaoman", OpID: strings.Repeat("a", 32), State: "succeeded", RuntimeRequested: true, OldCleanupState: "pending"}
	candidate := environment.Ref{Slot: environment.B, Revision: 1}
	r.parallelSwitchStatus = &environment.SwitchStatus{Phase: environment.Cleaning, Current: &candidate, Candidate: candidate, CurrentObserved: true, CurrentRunning: true, ConversationKnown: true, CleanupState: "pending", CanReviewOldCleanup: true}
	tt := ui.NewTester(a.View, 1180, 900)
	tt.Frame()
	tt.Advance(time.Second)
	for _, text := range []string{T("environment.switch_accepted"), T("runtime.conversation_confirmed"), T("environment.old_cleanup_pending"), T("environment.old_cleanup_review")} {
		if !tt.HasText(text) {
			t.Fatal("cleanup progress hid accepted environment", text, tt.Texts())
		}
	}
	if tt.HasText(T("environment.return_review")) || tt.HasText(T("environment.confirm_review")) {
		t.Fatal("partial deletion offered old environment or repeated dialogue")
	}
	shot(t, tt, "old-cleanup-pending")
	task := r.task
	a.restore, a.restoreTasks = nil, []service.RestoreTask{task}
	tt = ui.NewTester(func(c *ui.Context) { a.restoreBanner(c) }, 860, 560)
	tt.Frame()
	tt.Advance(time.Second)
	if !tt.HasText(T("environment.old_cleanup_banner", "target", "home / xiaoman")) {
		t.Fatal("cleanup disappeared because restoration was already successful")
	}
	click(t, tt, T("restore.open_task"))
	if a.restore == nil || a.restore.task.OpID != task.OpID || a.restore.j.busy || a.restore.oldCleanupPlan != nil || a.restore.oldCleanupConfirm {
		t.Fatal("reopening cleanup executed or reused consent")
	}
}
