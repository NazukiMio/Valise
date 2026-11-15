package gui

import (
	"github.com/egoist/mygo/ui"
	"slices"
	"testing"
	"time"
	"valise/core/environment"
	"valise/core/revive"
)

func TestManagedParallelPreviewNamesCurrentAndNextEnvironment(t *testing.T) {
	a, _, r := restoreWorld(t)
	r.step = "parallel_preview"
	r.parallelPlan = &revive.EnvironmentPlan{Mode: environment.Compact, ManagedSource: true, Original: &environment.Ref{Slot: environment.B, Revision: 1}, Candidate: &environment.Ref{Slot: environment.A, Revision: 4},
		ProgramVersion: "1.2.3", NativeRunning: true, NativeUnit: "fixture.service", ProgramMatches: true, ProgramFiles: 6, ProgramBytes: 2 << 20, Files: 12, Bytes: 1 << 20, LogicalRoots: 2, PhysicalRoots: 1, Space: []revive.EnvironmentSpace{{NeedBytes: 15 << 20, FreeBytes: 100 << 20}}}
	r.parallelPlan.Reuse = &revive.EnvironmentReuse{Target: environment.Ref{Slot: environment.A, Revision: 3}, Items: []environment.OldCleanupItem{
		{Kind: "data", Root: "state", Present: true, Files: 12, Bytes: 1 << 20},
		{Kind: "program", Present: true, Files: 6, Bytes: 2 << 20},
		{Kind: "preparation", Present: true, Files: 4, Bytes: 1 << 20},
	}}
	for _, dimension := range []struct {
		w, h int
		name string
	}{{860, 560, "managed-next-preview-small"}, {1180, 900, "managed-next-preview-large"}} {
		tt := ui.NewTester(a.View, dimension.w, dimension.h)
		tt.Frame()
		tt.Advance(time.Second)
		for _, text := range []string{T("environment.preview_managed"), T("environment.preview_from_slot"), T("environment.preview_to_slot"), T("environment.old_cleanup_slot", "slot", environment.B), T("environment.old_cleanup_slot", "slot", environment.A)} {
			if !tt.HasText(text) {
				t.Fatal("managed source or next slot missing", text, tt.Texts())
			}
		}
		if slices.Contains(tt.Texts(), T("environment.preview_native")) {
			t.Fatal("managed source described as native")
		}
		if !tt.HasText(T("environment.reuse_title", "slot", environment.A)) || !tt.HasText(T("environment.reuse_effect")) {
			t.Fatal("preview omitted reviewed inactive reuse", tt.Texts())
		}
		shot(t, tt, dimension.name)
		if dimension.h == 560 {
			tt.Scroll(600, 450, 0, 300)
			tt.Advance(time.Second)
			shot(t, tt, dimension.name+"-scrolled")
		}
	}
}

func TestParallelPreviewDisplaysConditionsAndNoExecutionControls(t *testing.T) {
	a, _, r := restoreWorld(t)
	r.step = "parallel_preview"
	r.parallelPlan = &revive.EnvironmentPlan{Mode: environment.Parallel, ProgramVersion: "1.2.3", NativeRunning: true, NativeUnit: "fixture.service", ProgramMatches: true, ProgramFiles: 6, ProgramBytes: 2 << 20, Files: 12, Bytes: 1 << 20, LogicalRoots: 2, PhysicalRoots: 1, ProfileUntested: true, Space: []revive.EnvironmentSpace{{NeedBytes: 15 << 20, FreeBytes: 100 << 20}}}
	tt := ui.NewTester(a.View, 860, 560)
	tt.Frame()
	tt.Advance(time.Second)
	for _, text := range []string{T("environment.preview_title"), T("environment.preview_native"), T("environment.preview_health"), T("environment.preview_only"), T("restore.edit")} {
		if !tt.HasText(text) {
			t.Fatal("missing preview evidence", text, tt.Texts())
		}
	}
	for _, text := range []string{T("restore.prepare"), T("restore.execute.rollback"), "确认可用", "清理旧环境"} {
		if tt.HasText(text) {
			t.Fatal("preview exposes execution", text)
		}
	}
	shot(t, tt, "parallel-restore-preview")
	click(t, tt, T("restore.edit"))
	if r.step != "setup" {
		t.Fatal("preview cannot return to selection")
	}
}

func TestParallelSetupUsesReadOnlyCapabilityAndHidesDirectPlan(t *testing.T) {
	a, tt, r := restoreWorld(t)
	r.restoreMode = &restoreModeState{started: true, planSupported: true, view: &environment.Settings{Mode: environment.Parallel}}
	tt.Frame()
	tt.Advance(time.Second)
	if !tt.HasText(T("environment.preview")) || tt.HasText(T("restore.view_plan")) {
		t.Fatal("parallel setup used the direct executor", tt.Texts())
	}
	r.restoreMode.view.Managed = true
	tt.Frame()
	tt.Advance(time.Second)
	if !tt.HasText(T("environment.preview")) || tt.HasText(T("restore.view_plan")) {
		t.Fatal("managed source lost its dedicated preview", tt.Texts())
	}
	r.restoreMode.planSupported = false
	tt.Frame()
	tt.Advance(time.Second)
	if tt.HasText(T("environment.preview")) || tt.HasText(T("restore.view_plan")) {
		t.Fatal("old porter draws a future operation")
	}
	_ = a
}
