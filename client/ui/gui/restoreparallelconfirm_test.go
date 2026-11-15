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

func TestParallelHumanAcceptanceNeedsItsOwnDialogueChoiceAndNeverDeletes(t *testing.T) {
	a, _, r := restoreWorld(t)
	r.step, r.parallelSwitchAction = "parallel_switch_review", "confirm"
	r.task = service.RestoreTask{Kind: service.RestoreKindEnvironment, Target: "home", Instance: "xiaoman", RuntimeRequested: true}
	candidate := environment.Ref{Slot: environment.B, Revision: 1}
	r.parallelSwitchPlan = &service.PreparedParallelSwitch{Expires: time.Now().Add(time.Minute), View: environment.SwitchReview{CanConfirm: true, ConfirmationDigest: strings.Repeat("d", 64), CandidateProgramChecked: true, ProfileUntested: true,
		Status: environment.SwitchStatus{Candidate: candidate, Current: &candidate, CurrentObserved: true, CurrentRunning: true, Phase: environment.AwaitingHuman, Mode: environment.Compact}}}
	for _, size := range []struct {
		width, height int
		name          string
	}{{860, 560, "parallel-confirm-small"}, {1180, 900, "parallel-confirm-large"}} {
		tt := ui.NewTester(a.View, size.width, size.height)
		tt.Frame()
		tt.Advance(time.Second)
		for _, text := range []string{T("environment.confirm_title"), T("environment.confirm_effect"), T("environment.confirm_dialogue"), T("environment.confirm_execute")} {
			if !tt.HasText(text) {
				t.Fatal("missing separate human acceptance", text, tt.Texts())
			}
		}
		for _, text := range []string{T("restore.allow_draft"), T("environment.switch_original_program"), T("environment.switch_stage"), T("environment.switch_execute"), T("environment.return_execute")} {
			if slices.Contains(tt.Texts(), text) {
				t.Fatal("acceptance borrowed another action or allowance", text)
			}
		}
		shot(t, tt, size.name)
		if size.height == 560 {
			tt.Scroll(600, 450, 0, 250)
			tt.Advance(time.Second)
			shot(t, tt, size.name+"-scrolled")
		}
		click(t, tt, T("environment.confirm_execute"))
		if r.j.busy || r.parallelSwitchConfirm {
			t.Fatal("acceptance skipped actual dialogue choice")
		}
	}
	tt := ui.NewTester(a.View, 1180, 900)
	tt.Frame()
	tt.Advance(time.Second)
	click(t, tt, T("environment.confirm_dialogue"))
	if !r.parallelSwitchConfirm || r.parallelSwitchDraft || r.j.busy {
		t.Fatal("dialogue choice executed or allowed another action")
	}
	r.parallelSwitchPlan.Expires = time.Now().Add(-time.Second)
	tt.Frame()
	tt.Advance(time.Second)
	if slices.Contains(tt.Texts(), T("environment.confirm_execute")) || !tt.HasText(T("restore.expired")) {
		t.Fatal("expired acceptance could run", tt.Texts())
	}
}

func TestParallelAcceptedStatusKeepsIndependentReturnUntilOriginalCleaned(t *testing.T) {
	a, _, r := restoreWorld(t)
	r.step, r.parallelRead = "parallel_preparation", true
	available := true
	r.parallelSwitchCapability = &available
	r.task = service.RestoreTask{Kind: service.RestoreKindEnvironment, RuntimeRequested: true}
	candidate := environment.Ref{Slot: environment.B, Revision: 1}
	r.parallelSwitchStatus = &environment.SwitchStatus{Phase: environment.Accepted, Current: &candidate, Candidate: candidate, CurrentObserved: true, CurrentRunning: true, ConversationKnown: true}
	tt := ui.NewTester(a.View, 860, 560)
	tt.Frame()
	tt.Advance(time.Second)
	if !tt.HasText(T("runtime.conversation_confirmed")) || !tt.HasText(T("environment.return_review")) || tt.HasText(T("environment.confirm_review")) {
		t.Fatal("acceptance deleted return or repeated human action", tt.Texts())
	}
	shot(t, tt, "parallel-confirmed-retained")
	r.parallelSwitchStatus.OriginalCleaned = true
	tt.Frame()
	tt.Advance(time.Second)
	if tt.HasText(T("environment.return_review")) {
		t.Fatal("cleaned environment offered return")
	}
}

func TestParallelAwaitingHumanBannerReopensWithoutConfirmingOrRunning(t *testing.T) {
	a, _ := world(t)
	task := service.RestoreTask{Kind: service.RestoreKindEnvironment, OpID: strings.Repeat("c", 32), Target: "home", Instance: "xiaoman", State: "awaiting_human", RuntimeRequested: true,
		Snapshot: journey.SnapshotRef{VaultID: "selected", SnapshotID: "point"}, SnapshotTier: "soul"}
	a.restoreTasks = []service.RestoreTask{task}
	tt := ui.NewTester(func(c *ui.Context) { a.restoreBanner(c) }, 860, 560)
	tt.Frame()
	tt.Advance(time.Second)
	if !tt.HasText(T("environment.awaiting_banner", "target", "home / xiaoman")) {
		t.Fatal("pending dialogue vanished after reopening", tt.Texts())
	}
	click(t, tt, T("restore.open_task"))
	r := a.restore
	if r == nil || r.step != "parallel_preparation" || r.task.OpID != task.OpID || r.point.SnapshotRef != task.Snapshot || r.j.busy || r.parallelSwitchPlan != nil || r.parallelSwitchConfirm {
		t.Fatal("opening pending dialogue executed or confirmed", r)
	}
}
