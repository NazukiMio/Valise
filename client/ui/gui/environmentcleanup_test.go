package gui

import (
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"valise/core/environment"
)

func TestSettingsCleanupIsSeparateFromModeSavingAndRequiresExecutionCapability(t *testing.T) {
	a, _ := world(t)
	a.restore = nil
	row := &agentRow{Server: "home", Instance: "xiaoman"}
	m := &restoreModeState{started: true, cleanupSupported: true, view: &environment.Settings{Mode: environment.Direct, Managed: true}}
	ps := &protState{restoreMode: m}
	tt := ui.NewTester(func(c *ui.Context) { a.restoreModeCard(c, row, ps) }, 860, 560)
	tt.Frame()
	tt.Advance(time.Second)
	if !tt.HasText(T("environment.old_cleanup_review")) || !tt.HasText(T("environment.settings_cleanup_effect")) || slices.Contains(tt.Texts(), T("environment.old_cleanup_execute")) || a.restore != nil {
		t.Fatal("saving direct mode was treated as deletion consent", tt.Texts())
	}
	shot(t, tt, "settings-cleanup-direct")
	for _, mode := range []environment.Mode{environment.Parallel, environment.Compact} {
		m.view.Mode = mode
		tt.Frame()
		tt.Advance(time.Second)
		if tt.HasText(T("environment.old_cleanup_review")) {
			t.Fatal("settings offered disabled-parallel cleanup in another mode", mode)
		}
	}
	m.view.Mode, m.cleanupSupported = environment.Direct, false
	tt.Frame()
	tt.Advance(time.Second)
	if tt.HasText(T("environment.old_cleanup_review")) {
		t.Fatal("old porter gained settings cleanup button")
	}
}
