package gui

import (
	"context"
	"testing"
	"time"

	"valise/core/journey"
	"valise/core/revive"
	"valise/core/valise"
	"valise/core/vault"
	"valise/ui/service"
)

func TestBundleRestoreAndMoveAllConfirmNotEmpty(t *testing.T) {
	a, tt := world(t)
	initTestKey(t, a)
	a.agents.value.Observations = []service.AgentServerObservation{
		{ConnectionID: "box", Status: "fresh"},
		{ConnectionID: "box2", Status: "fresh"},
	}

	// 1. openBundleRestoreFromFlow leaves Members empty when local timeline only
	// has a subset of members, so PrepareBundleRestore resolves all members.
	a.timeline.value = service.JourneyPage{
		Items: []journey.TimelinePoint{
			{
				SnapshotRef:        journey.SnapshotRef{SnapshotID: "s_main"},
				RecordedInstanceID: "inst-a",
				BundleID:           "b_partial",
				BundleMembers:      []string{"inst-a", "inst-b"},
			},
		},
	}
	rf := &restoreFlow{
		origin: "box",
		target: "box",
		mode:   revive.ModeRollback,
		point:  a.timeline.value.Items[0],
	}
	a.openBundleRestoreFromFlow(rf, "b_partial", []string{"inst-a", "inst-b"})
	if a.bundleRestore == nil || a.bundleRestore.bundleID != "b_partial" {
		t.Fatalf("expected bundleRestore to open with bundleID b_partial, got %+v", a.bundleRestore)
	}
	if len(a.bundleRestore.members) != 0 {
		t.Fatalf("expected Members to be cleared when timeline is partial, got %+v", a.bundleRestore.members)
	}

	// 2. Bundle restore blockers display, AllowDraft replan, secret + unverified
	// confirmation, and non-empty ReviveConfirm.
	var prepCalls []service.BundleRestoreOptions
	a.prepareBundleRestoreFn = func(_ context.Context, opts service.BundleRestoreOptions, _ string) (*service.BundleRestorePlan, error) {
		prepCalls = append(prepCalls, opts)
		mainPlan := &service.RevivePlan{
			Plan:          revive.Plan{InstanceID: "inst-a"},
			Warnings:      []string{"disk low warning"},
			RestoreSecret: true,
			UnverifiedSource: &service.UnverifiedSource{
				InstanceID:   "inst-a",
				ProfileID:    "demo",
				SourceServer: "box",
				Period:       "savepoints",
				CreatedAt:    time.Now(),
				Tier:         vault.TierFull,
				BackupFirst:  true,
			},
		}
		if !opts.AllowDraft {
			mainPlan.Blockers = []valise.UserError{*valise.New("profile_draft", "draft profile not verified", "")}
			mainPlan.Nested = []string{"nested-bot"}
		}
		return &service.BundleRestorePlan{
			BundleID: opts.BundleID,
			From:     opts.From,
			Target:   opts.Target,
			Mode:     opts.Mode,
			Order:    []string{"inst-a", "inst-b"},
			Plans: []*service.RevivePlan{
				mainPlan,
				{Plan: revive.Plan{InstanceID: "inst-b"}, DataOnly: true},
			},
		}, nil
	}
	var gotBundleConfirm service.ReviveConfirm
	var bundleRunCalls int
	a.runBundleRestoreFn = func(_ context.Context, _ *service.BundleRestorePlan, confirm service.ReviveConfirm) (*service.BundleRestoreResult, error) {
		bundleRunCalls++
		gotBundleConfirm = confirm
		return &service.BundleRestoreResult{
			BundleID: "b_partial",
			Members:  []service.BundleRestoreMemberResult{{InstanceID: "inst-a"}, {InstanceID: "inst-b"}},
		}, nil
	}

	a.router.Push("/restore")
	tt.Frame()
	if err := tt.Click(T("bundle.restore.prepare")); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText(T("bundle.member.blocked", "n", 2)) || !tt.HasText(T("restore.bg_notes_summary", "n", 2)) {
		t.Fatalf("expected honest blockers and two-layer background summary in bundle plan, got %q", tt.Texts())
	}
	if err := tt.Click(T("restore.tech_details")); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("draft profile not verified") || !tt.HasText("disk low warning") {
		t.Fatalf("expected expanded technical details to show draft reason and warning, got %q", tt.Texts())
	}
	if tt.HasText(T("bundle.member.main")) {
		t.Fatalf("blocked main member must not render green ok main role: %q", tt.Texts())
	}

	// Replan with AllowDraft enabled.
	if err := tt.Click(T("restore.allow_draft")); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click(T("restore.replan")); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(prepCalls) != 2 || !prepCalls[1].AllowDraft {
		t.Fatalf("expected second PrepareBundleRestore with AllowDraft=true, got %+v", prepCalls)
	}

	// Confirm secret, unverified source, and bundle check, then run.
	if err := tt.Click(T("restore.secret_confirm")); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click(T("restore.proceed")); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click(T("bundle.restore.confirm_check")); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click(T("bundle.restore.run")); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if bundleRunCalls != 1 {
		t.Fatalf("expected 1 bundle run call, got %d", bundleRunCalls)
	}
	if gotBundleConfirm.Swap == nil || !gotBundleConfirm.Swap(&revive.StageVerifyResult{}) || gotBundleConfirm.Swap(&revive.StageVerifyResult{Problems: []string{"corrupt"}}) {
		t.Fatalf("invalid Swap callback in bundle ReviveConfirm")
	}
	if gotBundleConfirm.UnverifiedSource == nil || gotBundleConfirm.UnverifiedSource(service.UnverifiedSource{}) != service.SourceProceed {
		t.Fatalf("expected SourceProceed in bundle ReviveConfirm")
	}

	// Verify real service.RunBundleRestore rejects empty ReviveConfirm with
	// confirmation_required, while our built ReviveConfirm passes Swap check and
	// invokes UnverifiedSource callback inside ExecuteReviveConfirmed.
	realBP := &service.BundleRestorePlan{
		BundleID: "b_partial",
		Plans: []*service.RevivePlan{{
			Plan:             revive.Plan{InstanceID: "inst-a"},
			UnverifiedSource: &service.UnverifiedSource{InstanceID: "inst-a", BackupFirst: false},
		}},
	}
	zeroRes, err := a.svc.RunBundleRestore(context.Background(), realBP, service.ReviveConfirm{})
	if err != nil || len(zeroRes.Members) != 1 || zeroRes.Members[0].ErrorCode != "confirmation_required" {
		t.Fatalf("expected confirmation_required for empty confirm, got res=%+v err=%v", zeroRes, err)
	}
	checkConfirm := (batchReviveOpts{sourceChoice: string(service.SourceBackupFirst)}).buildConfirm(true)
	realRes, err := a.svc.RunBundleRestore(context.Background(), realBP, checkConfirm)
	if err != nil {
		t.Fatalf("unexpected top-level RunBundleRestore error: %v", err)
	}
	if len(realRes.Members) != 1 || realRes.Members[0].ErrorCode != "backup_first_unavailable" {
		t.Fatalf("expected real RunBundleRestore to pass Swap check and invoke UnverifiedSource callback, got %+v", realRes.Members)
	}

	// 3. Move-all blockers display, AllowDraft replan, and non-empty ReviveConfirm.
	a.bundleRestore = nil
	a.agents.value.Items = nil
	a.agents.value.Observations = []service.AgentServerObservation{
		{ConnectionID: "box", Status: "fresh"},
		{ConnectionID: "box2", Status: "fresh"},
	}
	a.inspectHarnessMoveFn = func(_ context.Context, from, target string) (*service.HarnessMoveInspection, error) {
		return &service.HarnessMoveInspection{
			From:   from,
			Target: target,
			Groups: []service.HarnessMoveGroup{
				{ProfileID: "demo", Ready: true, Members: []service.HarnessMoveMember{{InstanceID: "demo", Ready: true, Snapshot: &vault.Meta{Tier: vault.TierFull}}}},
			},
		}, nil
	}
	var movePrep []service.HarnessMoveOptions
	a.prepareHarnessMoveFn = func(_ context.Context, o service.HarnessMoveOptions, _ string) (*service.HarnessMovePlan, error) {
		movePrep = append(movePrep, o)
		sp := &service.RevivePlan{Plan: revive.Plan{InstanceID: "demo"}}
		if !o.AllowDraft {
			sp.Blockers = []valise.UserError{*valise.New("profile_draft", "draft profile blocked", "")}
		}
		return &service.HarnessMovePlan{
			From:   o.From,
			Target: o.Target,
			Groups: []service.HarnessMoveGroupPlan{{
				ProfileID:  "demo",
				Group:      service.HarnessMoveGroup{ProfileID: "demo", Ready: true, Members: []service.HarnessMoveMember{{InstanceID: "demo"}}},
				SinglePlan: sp,
			}},
		}, nil
	}
	var gotMoveConfirm service.ReviveConfirm
	a.runHarnessMoveFn = func(_ context.Context, _ *service.HarnessMovePlan, confirm service.ReviveConfirm) (*service.HarnessMoveResult, error) {
		gotMoveConfirm = confirm
		return &service.HarnessMoveResult{From: "box", Target: "box2", Groups: []service.HarnessMoveGroupResult{{ProfileID: "demo", Succeeded: true}}}, nil
	}

	a.router.Push("/servers/box/move-all")
	tt.Advance(time.Second)
	tt.Frame()
	if err := tt.Click(T("moveall.inspect")); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click(T("moveall.prepare", "n", 1)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText(T("moveall.plan_blocked", "n", 1)) || tt.HasText(T("moveall.plan_ready", "n", 1)) {
		t.Fatalf("blocked move-all group must show blocked status, not ready: %q", tt.Texts())
	}
	if err := tt.Click(T("restore.allow_draft")); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click(T("restore.replan")); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(movePrep) != 2 || !movePrep[1].AllowDraft {
		t.Fatalf("expected move-all replan with AllowDraft=true, got %+v", movePrep)
	}
	if err := tt.Click(T("moveall.confirm_check")); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click(T("moveall.run")); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if gotMoveConfirm.Swap == nil || !gotMoveConfirm.Swap(&revive.StageVerifyResult{}) {
		t.Fatalf("expected valid non-nil Swap in move-all ReviveConfirm")
	}
}
