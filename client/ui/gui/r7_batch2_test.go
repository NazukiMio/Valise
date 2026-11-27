package gui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/enroll"
	"valise/core/profile"
	"valise/core/revive"
	"valise/core/valise"
	"valise/core/vault"
	"valise/ui/service"
)

func TestR7Batch2InlineInstallInBundleAndMoveAll(t *testing.T) {
	a, _ := world(t)
	if _, err := a.svc.KeyInit(testPass); err != nil {
		t.Fatal(err)
	}
	a.svc.BackUpKeyLater()
	tt := ui.NewTester(a.View, 1180, 1800)
	a.agents.value.Observations = []service.AgentServerObservation{
		{ConnectionID: "box", Status: "fresh"},
		{ConnectionID: "box2", Status: "fresh"},
	}

	// 1. Bundle restore with body_rebuild_unverified shows inline install and replans on completion.
	bundlePreps := 0
	a.prepareBundleRestoreFn = func(_ context.Context, opts service.BundleRestoreOptions, _ string) (*service.BundleRestorePlan, error) {
		bundlePreps++
		rp := &service.RevivePlan{
			Target: opts.Target,
			From:   opts.From,
			Plan:   revive.Plan{InstanceID: "inst-a", Profile: profile.Profile{ID: "demo-harness"}},
		}
		if bundlePreps == 1 {
			rp.Blockers = []valise.UserError{*valise.New("body_rebuild_unverified", "missing body", "")}
		}
		return &service.BundleRestorePlan{
			BundleID: "b1", From: opts.From, Target: opts.Target, Mode: opts.Mode,
			Order: []string{"inst-a"}, Plans: []*service.RevivePlan{rp},
		}, nil
	}
	a.bundleRestore = &bundleRestoreFlow{from: "box", target: "box2", mode: revive.ModeMigrate, bundleID: "b1"}
	a.router.Push("/restore")
	tt.Frame()
	if err := tt.Click(T("bundle.restore.prepare")); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText(T("restore.inline_install.title")) || !tt.HasText(T("restore.inline_install.prepare")) {
		t.Fatalf("bundle plan missing inline install box: %q", tt.Texts())
	}
	if a.bundleRestore.inlineInstall == nil || a.bundleRestore.inlineInstall.run == nil {
		t.Fatal("expected bundleRestore.inlineInstall to be initialized")
	}
	a.bundleRestore.inlineInstall.run.onDone(&service.InstallResult{Completed: true}, nil)
	tt.Frame()
	if bundlePreps != 2 {
		t.Fatalf("expected inline install completion to trigger bundle replan, got %d", bundlePreps)
	}

	// 2. Move-all plan with body_rebuild_unverified shows inline install and replans on completion.
	a.bundleRestore = nil
	movePreps := 0
	a.prepareHarnessMoveFn = func(_ context.Context, o service.HarnessMoveOptions, _ string) (*service.HarnessMovePlan, error) {
		movePreps++
		sp := &service.RevivePlan{
			Target: o.Target, From: o.From,
			Plan: revive.Plan{InstanceID: "inst-m", Profile: profile.Profile{ID: "demo-harness"}},
		}
		if movePreps == 1 {
			sp.Blockers = []valise.UserError{*valise.New("body_rebuild_unverified", "missing body", "")}
		}
		return &service.HarnessMovePlan{
			From: o.From, Target: o.Target,
			Groups: []service.HarnessMoveGroupPlan{{
				ProfileID:  "demo-harness",
				Group:      service.HarnessMoveGroup{ProfileID: "demo-harness", Ready: true, Members: []service.HarnessMoveMember{{InstanceID: "inst-m"}}},
				SinglePlan: sp,
			}},
		}, nil
	}
	st := a.moveAllFor("box")
	st.target = "box2"
	a.runPrepareHarnessMove(st, []string{"demo-harness"})
	a.router.Push("/servers/box/move-all")
	tt.Advance(time.Second)
	tt.Frame()
	if !tt.HasText(T("restore.inline_install.title")) {
		t.Fatalf("move-all plan missing inline install box: %q", tt.Texts())
	}
	slot := st.inlineInstalls["demo-harness"]
	if slot == nil || slot.run == nil {
		t.Fatal("expected moveAll inlineInstall slot for demo-harness")
	}
	slot.run.onDone(&service.InstallResult{Completed: true}, nil)
	tt.Frame()
	if movePreps != 2 {
		t.Fatalf("expected inline install completion to trigger move-all replan, got %d", movePreps)
	}
}

func TestR7Batch2CarryoverOptionsAndPostMigrate(t *testing.T) {
	a, _ := world(t)
	if _, err := a.svc.KeyInit(testPass); err != nil {
		t.Fatal(err)
	}
	a.svc.BackUpKeyLater()
	tt := ui.NewTester(a.View, 1180, 2200)
	a.agents.value.Observations = []service.AgentServerObservation{
		{ConnectionID: "box", Status: "fresh"},
		{ConnectionID: "box2", Status: "fresh"},
	}

	makeMigratePlan := func(id string) *service.RevivePlan {
		return &service.RevivePlan{
			From: "box", Target: "box2",
			HomeChanged: true, SourceHome: "/home/alice", TargetHome: "/home/bob",
			SourceProtection: &service.SourceProtectionSummary{Summary: "每天 03:00"},
			ExternalSymlinks: []service.ExternalSymlinkNote{{Root: "state", Path: "ext.lnk", Target: "/opt/ext"}},
			ConfigHomeHits:   []service.ConfigHomeHit{{Root: "state", Path: "cfg.json", Line: 12, OldPath: "/home/alice/.app", NewPath: "/home/bob/.app"}},
			Plan: revive.Plan{
				InstanceID: id,
				Mode:       revive.ModeMigrate,
				Profile: profile.Profile{
					ID:       "demo",
					AgentKit: &profile.AgentKit{MCPAddArgv: []string{"demo", "mcp", "add"}},
				},
			},
		}
	}

	samplePM := func() *service.PostMigrateReport {
		return &service.PostMigrateReport{
			ProtectionAttempted:     true,
			ProtectionCarried:       true,
			ProtectionNote:          "已沿用原保护计划",
			LocalRoots:              []enroll.LocalRoot{{ID: "extra", Path: "/data/extra", Category: "asset"}},
			RemappedSymlinks:        3,
			RewrittenConfigPaths:    2,
			RemainingConfigHomeHits: 1,
			MCPAutoRan:              true,
			MCPWireOutcome:          "failed",
			MCPCanWire:              true,
			MCPNeedsRewire:          true,
			MCPWireResult:           &service.KitStepOutcome{Attempted: true, OK: false, ExitCode: 1, Err: "cmd failed"},
			MCPServiceRestart:       &service.KitServiceRestartOutcome{Unit: "demo.service", Attempted: true, OK: true, Healthy: true},
			ToolPresent:             []string{"git"},
			ToolMissing:             []enroll.ToolDep{{Name: "rg", Purpose: "search"}},
			ExternalRefs:            []enroll.ExternalRef{{Path: "/opt/shared", Reason: "external"}},
		}
	}

	a.prepareBundleRestoreFn = func(_ context.Context, opts service.BundleRestoreOptions, _ string) (*service.BundleRestorePlan, error) {
		return &service.BundleRestorePlan{
			BundleID: "b2", From: opts.From, Target: opts.Target, Mode: opts.Mode,
			Order: []string{"inst-a"}, Plans: []*service.RevivePlan{makeMigratePlan("inst-a")},
		}, nil
	}
	var gotConfirm service.ReviveConfirm
	a.runBundleRestoreFn = func(_ context.Context, _ *service.BundleRestorePlan, confirm service.ReviveConfirm) (*service.BundleRestoreResult, error) {
		gotConfirm = confirm
		return &service.BundleRestoreResult{
			BundleID: "b2",
			Members:  []service.BundleRestoreMemberResult{{InstanceID: "inst-a", PostMigrate: samplePM()}},
		}, nil
	}
	rewireCalls := 0
	a.rewireMigratedMCP = func(_ context.Context, host, inst string) (*service.KitPlan, error) {
		rewireCalls++
		return &service.KitPlan{
			MCPWire:        &service.KitStepOutcome{Attempted: true, OK: true},
			ServiceRestart: &service.KitServiceRestartOutcome{Unit: "demo.service", Attempted: true, OK: true, Healthy: true},
		}, nil
	}

	a.bundleRestore = &bundleRestoreFlow{from: "box", target: "box2", mode: revive.ModeMigrate, bundleID: "b2"}
	a.router.Push("/restore")
	tt.Frame()
	click(t, tt, T("bundle.restore.prepare"))

	// Verify single-agent-parity defaults are set.
	opts := a.bundleRestore.opts
	if !opts.carryMCP || !opts.remapHomeSymlinks || !opts.carryProtection || opts.rewriteConfigHomePaths {
		t.Fatalf("unexpected migrate carryover defaults: %+v", opts)
	}
	hasAll(t, tt.Texts(),
		T("restore.ext_symlinks.list", "n", 1),
		T("restore.home.carry_mcp"),
		T("restore.home.remap_symlinks", "old", "/home/alice", "new", "/home/bob"),
		T("restore.home.rewrite_config", "n", 1),
		"inst-a: state/cfg.json:L12: /home/alice/.app -> /home/bob/.app",
	)

	// Opt in to config home path rewrite and run.
	click(t, tt, T("restore.home.rewrite_config", "n", 1))
	click(t, tt, T("bundle.restore.confirm_check"))
	click(t, tt, T("bundle.restore.run"))
	if !gotConfirm.CarryMCP || !gotConfirm.RemapHomeSymlinks || !gotConfirm.RewriteConfigHomePaths || !gotConfirm.CarryProtection {
		t.Fatalf("carryover flags not passed in ReviveConfirm: %+v", gotConfirm)
	}

	// Verify PostMigrateReport rendering and MCP rewire retry.
	hasAll(t, tt.Texts(),
		T("bundle.post.member_title", "id", "inst-a"),
		T("restore.post.remapped_symlinks_val", "n", 3),
		T("restore.post.rewritten_paths_val", "n", 2),
		T("restore.post.remaining_home_hits", "n", 1),
		T("restore.post.tools_missing_count", "n", 1),
		"rg", "/opt/shared", "/data/extra",
		T("restore.post.rewire_mcp"),
	)
	click(t, tt, T("restore.post.rewire_mcp"))
	if rewireCalls != 1 {
		t.Fatalf("expected RewireMigratedMCP retry call, got %d", rewireCalls)
	}

	// Verify move-all also passes carryover flags and renders PostMigrateReport.
	a.bundleRestore = nil
	a.prepareHarnessMoveFn = func(_ context.Context, o service.HarnessMoveOptions, _ string) (*service.HarnessMovePlan, error) {
		return &service.HarnessMovePlan{
			From: o.From, Target: o.Target,
			Groups: []service.HarnessMoveGroupPlan{{
				ProfileID:  "demo",
				Group:      service.HarnessMoveGroup{ProfileID: "demo", Ready: true, Members: []service.HarnessMoveMember{{InstanceID: "inst-m"}}},
				SinglePlan: makeMigratePlan("inst-m"),
			}},
		}, nil
	}
	var gotMoveConfirm service.ReviveConfirm
	a.runHarnessMoveFn = func(_ context.Context, _ *service.HarnessMovePlan, confirm service.ReviveConfirm) (*service.HarnessMoveResult, error) {
		gotMoveConfirm = confirm
		return &service.HarnessMoveResult{
			From: "box", Target: "box2",
			Groups: []service.HarnessMoveGroupResult{{
				ProfileID: "demo", Succeeded: true,
				Members: []service.BundleRestoreMemberResult{{InstanceID: "inst-m", PostMigrate: samplePM()}},
			}},
		}, nil
	}
	st := a.moveAllFor("box")
	st.target = "box2"
	a.runPrepareHarnessMove(st, []string{"demo"})
	a.router.Push("/servers/box/move-all")
	tt.Advance(time.Second)
	tt.Frame()
	click(t, tt, T("moveall.confirm_check"))
	click(t, tt, T("moveall.run"))
	if !gotMoveConfirm.CarryMCP || !gotMoveConfirm.RemapHomeSymlinks || !gotMoveConfirm.CarryProtection {
		t.Fatalf("move-all carryover flags not passed: %+v", gotMoveConfirm)
	}
	hasAll(t, tt.Texts(), T("bundle.post.member_title", "id", "inst-m"), T("restore.post.rewire_mcp"), "rg")
}

func TestR7Batch2TwoLayerDenoiseKeepsConfirmations(t *testing.T) {
	_, tt, r := restoreWorld(t)
	r.step = "plan"
	r.plan = &service.PreparedRestore{Revive: &service.RevivePlan{
		Target: "home", From: "home", RestoreSecret: true,
		Warnings: []string{"non-blocking env note"},
		UnverifiedSource: &service.UnverifiedSource{
			InstanceID: "xiaoman", ProfileID: "demo", ProfileVersion: 1,
			SourceServer: "home", Period: "savepoints", CreatedAt: time.Now(), Tier: vault.TierFull,
		},
		Plan: revive.Plan{Mode: revive.ModeRollback, InstanceID: "xiaoman", Tier: "full", Body: revive.BodyPlan{Action: revive.BodyKeep}},
		Blockers: []valise.UserError{
			{Code: "profile_draft", Reason: "verbose draft explanation"},
		},
	}}
	tt.Frame()
	tt.Advance(time.Second)

	// Required confirmations stay directly visible, never folded.
	hasAll(t, tt.Texts(),
		T("restore.warnings_summary", "n", 1),
		T("restore.unverified_summary"),
		T("restore.secret_confirm"),
		T("restore.proceed"),
		T("restore.allow_draft"),
		T("restore.tech_details"),
	)
	// Verbose background details are folded by default.
	if tt.HasText("non-blocking env note") || tt.HasText("verbose draft explanation") {
		t.Fatalf("expected non-blocking background details to be folded by default: %q", tt.Texts())
	}
}

func TestR7Batch2AnimatedMultiStageProgress(t *testing.T) {
	a, _ := world(t)
	tr := restoreProgressTracker{}
	ctx := a.withProgressTracker(context.Background(), &tr)
	// Deliver StageBundleMember and StageVerify + bytes via service.WithProgress.
	if fn, ok := ctx.Value(struct{}{}).(func(service.Progress)); ok {
		_ = fn
	}
	tr.apply(service.Progress{Stage: service.StageBundleMember, Done: 2, Total: 3})
	tr.apply(service.Progress{Stage: service.StageVerify, Done: 512, Total: 1024})

	started := time.Now().Add(-5 * time.Second)
	tt := ui.NewTester(func(c *ui.Context) {
		renderRestoreAnimProgress(c, tr, started, "", T("bundle.restore.running"))
	}, 960, 480)
	tt.Frame()
	hasAll(t, tt.Texts(),
		T("restore.anim.stage_pull"),
		T("restore.anim.stage_stage"),
		T("restore.anim.stage_verify"),
		T("restore.anim.stage_swap"),
		T("restore.anim.stage_finalize"),
		T("restore.anim.member_progress", "done", 2, "total", 3),
		T("restore.bytes", "done", size(512), "total", size(1024)),
	)
	for i := 0; i < 4; i++ {
		tt.Advance(150 * time.Millisecond)
		tt.Frame()
	}
}

func TestR7Batch2FileLineLimitAndCatalog(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Count(string(b), "\n")
		if lines > 400 {
			t.Errorf("%s has %d lines (max 400)", f, lines)
		}
	}
	for _, key := range []string{
		"restore.tech_details", "restore.warnings_summary", "restore.bg_notes_summary",
		"restore.unverified_summary", "restore.anim.stage_pull", "restore.anim.stage_stage",
		"restore.anim.stage_verify", "restore.anim.stage_swap", "restore.anim.stage_finalize",
		"restore.anim.member_progress", "bundle.restore.running", "moveall.running",
		"bundle.post.member_title",
	} {
		if _, ok := baseCatalog.Msgs[key]; !ok {
			t.Errorf("missing i18n key %q", key)
		}
	}
}
