package gui

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"valise/core/environment"
	"valise/ui/service"
)

func TestParallelHistoricalResultHasNoCurrentFactsOrActions(t *testing.T) {
	a, _, r := restoreWorld(t)
	r.step, r.parallelRead = "parallel_preparation", true
	available := true
	r.parallelSwitchCapability = &available
	r.task = service.RestoreTask{Kind: service.RestoreKindEnvironment, RuntimeRequested: true}
	r.parallelSwitchStatus = &environment.SwitchStatus{Mode: environment.Parallel, Phase: environment.Accepted, Historical: true, HistoricalConfirmed: true}
	for _, window := range []struct{ w, h int }{{860, 560}, {1180, 900}} {
		tt := ui.NewTester(a.View, window.w, window.h)
		tt.Frame()
		tt.Advance(time.Second)
		if !tt.HasText(T("environment.historical_hint")) || !tt.HasText(T("environment.historical_confirmed")) {
			t.Fatal("historical result lost", tt.Texts())
		}
		for _, key := range []string{"runtime.running", "runtime.run_unknown", "runtime.gateway_pending", "runtime.conversation_pending", "environment.return_review", "environment.confirm_review", "environment.switch_review", "environment.old_cleanup_review"} {
			if tt.HasText(T(key)) {
				t.Fatal("historical record offered current fact or action", key)
			}
		}
		shot(t, tt, "parallel-historical-result")
	}
}
