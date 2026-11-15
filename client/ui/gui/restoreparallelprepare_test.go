package gui

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"valise/core/environment"
	"valise/core/revive"
	"valise/ui/service"
)

func TestParallelPreparationChoicesAreSeparateAndCapabilityGated(t *testing.T) {
	a, _, r := restoreWorld(t)
	r.step = "parallel_preview"
	r.parallelPrepared = &service.PreparedParallelRestore{View: revive.EnvironmentPlan{Mode: environment.Parallel, ProfileUntested: true}, RequiresCredentials: true, Materials: &service.TestMaterialReview{}, Expires: time.Now().Add(time.Minute)}
	r.parallelPlan = &r.parallelPrepared.View
	r.restoreMode.prepareSupported = true
	// Render just the actual choice card so all controls fit in the minimum
	// window; its parent preview already has scrolling and a separate test.
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Width(800).Padding(16).Gap(10).Children(func() { a.parallelPrepareChoices(c, r) })
	}, 860, 560)
	tt.Frame()
	tt.Advance(time.Second)
	for _, text := range []string{T("environment.prepare_effect"), T("restore.secret_confirm"), T("restore.allow_draft"), T("environment.prepare_baseline_confirm"), T("environment.prepare")} {
		if !tt.HasText(text) {
			t.Fatal("missing preparation impact", text, tt.Texts())
		}
	}
	click(t, tt, T("environment.prepare"))
	if r.step != "parallel_preview" || r.j.busy {
		t.Fatal("preparation skipped separate choices")
	}
	click(t, tt, T("restore.secret_confirm"))
	if !r.credential || r.draft || r.parallelBaseline {
		t.Fatal("one choice authorized other impacts")
	}
	shot(t, tt, "parallel-prepare-choices")
	r.parallelPrepared.Expires = time.Now().Add(-time.Minute)
	tt.Frame()
	tt.Advance(time.Second)
	if tt.HasText(T("environment.prepare")) || !tt.HasText(T("restore.expired")) {
		t.Fatal("expired plan can execute")
	}
	// The full preview must keep its old read-only behavior for an old porter.
	r.restoreMode.prepareSupported = false
	tt = ui.NewTester(a.View, 860, 560)
	tt.Frame()
	tt.Advance(time.Second)
	if tt.HasText(T("environment.prepare")) || !tt.HasText(T("environment.preview_only")) {
		t.Fatal("old porter gained a preparation button", tt.Texts())
	}
}

func TestParallelPreparationStatusDoesNotOfferSwitchOrAutomaticCleanup(t *testing.T) {
	a, _, r := restoreWorld(t)
	r.step, r.parallelRead = "parallel_preparation", true
	r.task = service.RestoreTask{Kind: service.RestoreKindEnvironment, OpID: strings.Repeat("a", 32), Target: "home", Instance: "xiaoman", State: "prepared", Verified: true}
	r.parallelStatus = &environment.PreparationStatus{State: "prepared", Mode: environment.Parallel, OriginalObserved: true, OriginalRunning: true, CanCancel: true, Probe: "stopped",
		Check: &environment.ProbeResult{Completed: true, ProcessStarted: true, PortListening: true, HTTPResponded: true, GatewayPassed: true, ProcessesStopped: true}}
	tt := ui.NewTester(a.View, 860, 560)
	tt.Frame()
	tt.Advance(time.Second)
	for _, text := range []string{T("environment.preparation_state.prepared"), T("runtime.running"), T("environment.prepare_cancel"), T("environment.prepare_no_switch")} {
		if !tt.HasText(text) {
			t.Fatal("missing observed preparation state", text, tt.Texts())
		}
	}
	for _, text := range []string{T("restore.execute.rollback"), "确认可用", "清理旧环境", T("environment.prepare_cleanup")} {
		if tt.HasText(text) {
			t.Fatal("preparation claims unavailable later operation", text)
		}
	}
	shot(t, tt, "parallel-prepare-ready")
	large := ui.NewTester(a.View, 1180, 900)
	large.Frame()
	large.Advance(time.Second)
	shot(t, large, "parallel-prepare-ready-large")
	r.parallelStatus.State = "cancelled"
	r.parallelStatus.CanCancel, r.parallelStatus.CanReviewCleanup = false, true
	tt.Frame()
	tt.Advance(time.Second)
	if !tt.HasText(T("environment.prepare_review_cleanup")) || tt.HasText(T("environment.prepare_cleanup")) {
		t.Fatal("cancel skipped independent cleanup review", tt.Texts())
	}
	r.parallelCleanup = &environment.PreparationCleanupReview{Items: []environment.PreparationCleanupItem{{Kind: "program-original", Present: true}, {Kind: "workspace", Present: true}}}
	tt.Frame()
	tt.Advance(time.Second)
	shot(t, tt, "parallel-prepare-cleanup")
	if !tt.HasText(T("environment.material.original")) || !tt.HasText(T("environment.prepare_cleanup")) {
		t.Fatal("cleanup lacks explicit material scope", tt.Texts())
	}
}

func TestParallelPreparationHistoryReopensExactPointAndOwnController(t *testing.T) {
	a, _, _ := restoreWorld(t)
	at := time.Date(2026, 10, 1, 12, 30, 0, 0, time.UTC)
	a.restoreTasks = []service.RestoreTask{{Kind: service.RestoreKindEnvironment, OpID: strings.Repeat("d", 32), Target: "home", Mode: revive.ModeRollback, Instance: "xiaoman", State: "prepared", SnapshotTime: at, SnapshotTier: "soul"}}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Width(800).Padding(16).Children(func() { a.restoreTaskList(c) })
	}, 860, 560)
	tt.Frame()
	tt.Advance(time.Second)
	click(t, tt, T("restore.open_task"))
	if r := a.restore; r == nil || r.step != "parallel_preparation" || r.point.CreatedAt != at || r.point.Scope.Tier != "soul" {
		t.Fatal("history routed to old swap or lost selected point", r)
	}
}
