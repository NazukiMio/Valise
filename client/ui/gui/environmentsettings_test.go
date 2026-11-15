package gui

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"valise/core/environment"
	"valise/core/identity"
)

func TestRestoreModeCardDraftIsSeparateFromSavedAndNoCleanup(t *testing.T) {
	a, _ := world(t)
	ref := identity.AgentRef{ServerUID: "server", InstallID: "install", InstanceID: "agent", Generation: "one"}
	v := environment.Settings{Agent: ref, Mode: environment.Parallel}
	v.Digest = v.Stamp()
	ps := &protState{ref: &ref, restoreMode: &restoreModeState{view: &v, draft: v.Mode, started: true}}
	row := &agentRow{Server: "home", Instance: "agent"}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Width(800).Padding(16).Children(func() { a.restoreModeCard(c, row, ps) })
	}, 860, 560)
	tt.Frame()
	tt.Advance(time.Second)
	click(t, tt, T("environment.edit"))
	for _, text := range []string{T("environment.settings"), T("environment.mode.parallel"), T("environment.mode.compact"), T("environment.mode.direct"), T("environment.parallel_pending")} {
		if !tt.HasText(text) {
			t.Fatal("missing", text, tt.Texts())
		}
	}
	if tt.HasText(T("environment.save")) {
		t.Fatal("unchanged settings can save")
	}
	click(t, tt, T("environment.mode.compact"))
	if ps.restoreMode.draft != environment.Compact || ps.restoreMode.view.Mode != environment.Parallel || !tt.HasText(T("environment.save")) {
		t.Fatal("draft changed saved policy")
	}
	shot(t, tt, "environment-settings-compact")
	ps.restoreMode.view.Busy = true
	tt.Frame()
	tt.Advance(time.Second)
	if tt.HasText(T("environment.save")) || !tt.HasText(T("environment.busy")) {
		t.Fatal("in-flight mode is editable")
	}
	// Neither a setting change nor this card offers a premature file cleanup.
	if tt.HasText("清理旧环境") || tt.HasText("确认可用") {
		t.Fatal("unimplemented execution rendered")
	}
}

func TestRestorePolicyNeverOffersDirectExecutionForParallelOrUnknown(t *testing.T) {
	a, _, r := restoreWorld(t)
	v := environment.Settings{Mode: environment.Parallel}
	r.restoreMode = &restoreModeState{view: &v, started: true}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Width(800).Padding(16).Children(func() { a.restorePolicyPanel(c, r) })
	}, 860, 560)
	tt.Frame()
	tt.Advance(time.Second)
	if directRestoreReady(r.restoreMode) || !tt.HasText(T("environment.parallel_pending")) {
		t.Fatal("parallel allowed legacy execution")
	}
	shot(t, tt, "restore-policy-parallel")
	for _, mode := range []environment.Mode{environment.Parallel, environment.Compact} {
		v.Mode = mode
		if directRestoreReady(r.restoreMode) {
			t.Fatal(mode)
		}
	}
	v.Mode = environment.Direct
	if !directRestoreReady(r.restoreMode) {
		t.Fatal("explicit direct unavailable")
	}
	v.Managed = true
	if directRestoreReady(r.restoreMode) {
		t.Fatal("managed direct used legacy executor")
	}
	v.Managed, v.Busy = false, true
	if directRestoreReady(r.restoreMode) {
		t.Fatal("busy runtime used legacy executor")
	}
	if directRestoreReady(nil) {
		t.Fatal("unknown settings used direct executor")
	}
}
