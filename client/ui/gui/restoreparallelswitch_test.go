package gui

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"valise/core/environment"
	"valise/core/journey"
	"valise/ui/service"
)

func TestParallelUncertainBannerReopensParentAndPreservesPoint(t *testing.T) {
	a, _ := world(t)
	at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	task := service.RestoreTask{Kind: service.RestoreKindEnvironment, RuntimeRequested: true, Target: "home", OpID: strings.Repeat("b", 32), State: "reconciling",
		SnapshotTime: at, SnapshotTier: "soul", Snapshot: journey.SnapshotRef{VaultID: "selected-vault", SnapshotID: "selected-point"}}
	a.restoreTasks = []service.RestoreTask{task}
	tt := ui.NewTester(func(c *ui.Context) { a.restoreBanner(c) }, 860, 560)
	tt.Frame()
	tt.Advance(time.Second)
	click(t, tt, T("restore.open_task"))
	r := a.restore
	if r == nil || r.step != "parallel_preparation" || !r.task.RuntimeRequested || r.point.SnapshotRef != task.Snapshot || r.point.CreatedAt != at || r.point.Scope.Tier != "soul" {
		t.Fatal("uncertain parent routed to old recovery or lost point", r)
	}
	r.step, r.parallelSwitchAction, r.j.busy = "swap", "return", true
	closed := false
	if a.RequestClose(func() { closed = true }) || closed {
		t.Fatal("normal close interrupted formal return")
	}
	if restoreStepLabel(r) != T("environment.return_progress") {
		t.Fatal("return shown as preparation")
	}
	r.j.busy = false
	a.restoreSafeExit(r)
	if !closed {
		t.Fatal("safe return completion did not finish close")
	}
}

func TestParallelSwitchReviewSeparatesDraftAndCutoverConfirmation(t *testing.T) {
	a, _, r := restoreWorld(t)
	r.step, r.parallelSwitchAction = "parallel_switch_review", "switch"
	r.task.Target, r.task.Instance = "home", "xiaoman"
	r.parallelSwitchPlan = &service.PreparedParallelSwitch{Expires: time.Now().Add(time.Minute), View: environment.SwitchReview{CanSwitch: true, ProfileUntested: true,
		OriginalProgramChecked: true, CandidateProgramChecked: true, SourceChecked: true, StageChecked: true, Files: 3, Bytes: 128, Status: environment.SwitchStatus{Mode: environment.Parallel}}}
	// The review body has a scrolling parent in the actual restore route.
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Scroll(c).Children(func() {
			ui.Column(c).Width(800).Padding(16).Gap(10).Children(func() { a.parallelSwitchReview(c, r) })
		})
	}, 860, 560)
	tt.Frame()
	tt.Advance(time.Second)
	for _, text := range []string{T("environment.switch_original_program"), T("environment.switch_candidate_program"), T("environment.switch_stage"), T("environment.switch_source"), T("environment.switch_effect"), T("restore.allow_draft"), T("environment.switch_confirm")} {
		if !tt.HasText(text) {
			t.Fatal("missing fresh cutover fact", text, tt.Texts())
		}
	}
	shot(t, tt, "parallel-switch-review-small")
	large := ui.NewTester(a.View, 1180, 900)
	large.Frame()
	large.Advance(time.Second)
	shot(t, large, "parallel-switch-review-large")
	small := ui.NewTester(a.View, 860, 560)
	small.Frame()
	small.Advance(time.Second)
	shot(t, small, "parallel-switch-review-full-small")
	// No service call occurs with only one of the two independent choices.
	click(t, tt, T("environment.switch_execute"))
	if r.j.busy || r.parallelSwitchConfirm || r.parallelSwitchDraft {
		t.Fatal("cutover skipped confirmation")
	}
	click(t, tt, T("environment.switch_confirm"))
	click(t, tt, T("environment.switch_execute"))
	if !r.parallelSwitchConfirm || r.parallelSwitchDraft || r.j.busy {
		t.Fatal("cutover confirmation allowed untested capability")
	}
	r.parallelSwitchPlan.Expires = time.Now().Add(-time.Second)
	tt.Frame()
	tt.Advance(time.Second)
	if !tt.HasText(T("restore.expired")) || tt.HasText(T("environment.switch_execute")) {
		t.Fatal("expired review offers execution", tt.Texts())
	}
}

func TestParallelSwitchStatusSeparatesGatewayDialogueAndCapability(t *testing.T) {
	a, _, r := restoreWorld(t)
	r.step, r.parallelRead = "parallel_preparation", true
	closed := false
	r.parallelSwitchCapability = &closed
	r.task = service.RestoreTask{Kind: service.RestoreKindEnvironment, RuntimeRequested: true}
	candidate := environment.Ref{Slot: environment.B, Revision: 1}
	r.parallelSwitchStatus = &environment.SwitchStatus{Mode: environment.Parallel, Phase: environment.AwaitingHuman, Current: &candidate, CurrentObserved: true, CurrentRunning: true,
		GatewayObserved: true, PortOwned: true, HTTPResponded: true, GatewayPassed: true}
	for _, window := range []struct {
		width, height int
		name          string
	}{{860, 560, "parallel-switch-status-small"}, {1180, 900, "parallel-switch-status-large"}} {
		tt := ui.NewTester(a.View, window.width, window.height)
		tt.Frame()
		tt.Advance(time.Second)
		for _, text := range []string{T("environment.switch_awaiting_human"), T("runtime.running"), T("runtime.gateway_passed"), T("runtime.conversation_pending")} {
			if !tt.HasText(text) {
				t.Fatal("missing separate runtime fact", text, tt.Texts())
			}
		}
		for _, text := range []string{T("environment.return_review"), T("runtime.conversation_confirmed"), T("environment.preparation_state.prepared"), T("environment.prepare_cleanup")} {
			if tt.HasText(text) {
				t.Fatal("unavailable action or borrowed fact", text)
			}
		}
		shot(t, tt, window.name)
		open := true
		r.parallelSwitchCapability = &open
		tt.Frame()
		tt.Advance(time.Second)
		if !tt.HasText(T("environment.return_review")) {
			t.Fatal("supported return has no review entry", tt.Texts())
		}
		r.parallelSwitchStatus.GatewayObserved = false
		tt.Frame()
		tt.Advance(time.Second)
		if tt.HasText(T("runtime.gateway_passed")) {
			t.Fatal("unobserved gateway was green")
		}
		r.parallelSwitchStatus.GatewayObserved = true
		r.parallelSwitchCapability = &closed
	}
	// Reconnection without current observations must not reuse preparation's
	// old positive probe or offer an automatic retry of the switch.
	r.parallelSwitchStatus = nil
	tt := ui.NewTester(a.View, 860, 560)
	tt.Frame()
	tt.Advance(time.Second)
	if !tt.HasText(T("environment.switch_unknown")) || tt.HasText(T("environment.switch_review")) || tt.HasText(T("environment.prepare_title")) {
		t.Fatal("uncertain switch fell back to preparation", tt.Texts())
	}
}

func TestParallelReturnReviewHasItsOwnImpactAndNoDraftChoice(t *testing.T) {
	a, _, r := restoreWorld(t)
	r.step, r.parallelSwitchAction = "parallel_switch_review", "return"
	r.task.Target, r.task.Instance = "home", "xiaoman"
	r.parallelSwitchPlan = &service.PreparedParallelSwitch{Expires: time.Now().Add(time.Minute), View: environment.SwitchReview{CanReturn: true, OriginalProgramChecked: true,
		Status: environment.SwitchStatus{Mode: environment.Parallel}}}
	tt := ui.NewTester(a.View, 1180, 900)
	tt.Frame()
	tt.Advance(time.Second)
	for _, text := range []string{T("environment.return_effect"), T("environment.return_confirm"), T("environment.return_execute")} {
		if !tt.HasText(text) {
			t.Fatal("return lacks own confirmation", text, tt.Texts())
		}
	}
	if tt.HasText(T("restore.allow_draft")) || tt.HasText(T("environment.switch_stage")) || tt.HasText(T("environment.switch_execute")) {
		t.Fatal("return borrowed cutover requirements", tt.Texts())
	}
	shot(t, tt, "parallel-return-review-large")
	r.parallelSwitchPlan.View.CanReturn = false
	tt.Frame()
	tt.Advance(time.Second)
	if slices.Contains(tt.Texts(), T("environment.return_execute")) || !tt.HasText(T("environment.return_not_ready")) {
		t.Fatal("unready return can execute", tt.Texts())
	}
}
