package gui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"valise/core/enroll"
	"valise/core/identity"
	"valise/core/journey"
	"valise/core/protection"
	"valise/core/revive"
	"valise/core/valise"
	"valise/core/vault"
	"valise/core/vaultrpc"
	"valise/ui/service"
)

func TestWP2SnapshotDeletePreviewAndResults(t *testing.T) {
	a, tt := world(t)
	now := time.Now()
	a.timeline.value = service.JourneyPage{
		Items: []journey.TimelinePoint{
			{
				SnapshotRef:        journey.SnapshotRef{VaultID: "v1", SnapshotID: "s_01"},
				CreatedAt:          now.Add(-time.Hour),
				RecordedInstanceID: "openclaw",
				Scope:              journey.Scope{Kind: "preset", Tier: vault.TierFull},
				Kind:               vault.KindSavepoint,
				BundleID:           "b_01",
				BundleMembers:      []string{"openclaw", "openclaw-sub"},
				Copies: []journey.Copy{
					{LocationRef: journey.LocationRef{Kind: "server_vault", ID: "box"}, SourceLocator: "box/openclaw/savepoints"},
					{LocationRef: journey.LocationRef{Kind: "local_repo", ID: "disk"}, SourceLocator: "box/openclaw/savepoints"},
				},
			},
		},
	}
	var previewCalled, deleteCalled bool
	a.previewSnapDeleteFn = func(_ context.Context, req service.SnapshotDeleteRequest) (*service.SnapshotDeletePreview, error) {
		previewCalled = true
		return &service.SnapshotDeletePreview{
			Host:        req.Host,
			Leaf:        req.Leaf,
			SnapshotIDs: req.SnapshotIDs,
			Locations:   req.Locations,
			BundleImpacts: []service.SnapshotBundleWarning{
				{SnapshotID: "s_01", BundleID: "b_01", BundleMembers: []string{"openclaw", "openclaw-sub"}},
			},
		}, nil
	}
	a.deleteSnapshotsFn = func(_ context.Context, req service.SnapshotDeleteRequest) (*service.SnapshotDeleteBatchResult, error) {
		deleteCalled = true
		return &service.SnapshotDeleteBatchResult{
			Host: req.Host,
			Leaf: req.Leaf,
			Results: []service.SnapshotDeleteLocationResult{
				{Location: service.SnapshotDeleteLocation{Kind: service.SnapshotLocationServerVault}, Deleted: 1, PacksRemoved: 2, FreedBytes: 2048},
			},
		}, nil
	}

	a.openAgent("box", "openclaw")
	a.agentTab = tabJourney
	tt.Frame()
	if !tt.HasText(T("bundle.point_badge", "members", "openclaw-sub")) {
		t.Fatalf("missing bundle point badge in %q", tt.Texts())
	}

	// Open single-point delete modal, check server_vault location, preview, confirm.
	a.openSinglePointDelete(&agentRow{Server: "box", Instance: "openclaw"}, a.timeline.value.Items[0])
	tt.Frame()
	if err := tt.Click(T("snapdel.loc.server_vault")); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click(T("snapdel.preview_go")); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !previewCalled {
		t.Fatal("expected PreviewSnapshotDelete to be called")
	}
	if !tt.HasText(T("snapdel.bundle_impact", "id", "s_01", "members", "openclaw、openclaw-sub")) {
		t.Fatalf("missing bundle impact warning in %q", tt.Texts())
	}
	if err := tt.Click(T("snapdel.confirm_go")); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !deleteCalled {
		t.Fatal("expected DeleteSnapshots to be called")
	}
	if !tt.HasText(T("snapdel.loc_ok", "deleted", 1, "packs", 2, "freed", size(2048))) {
		t.Fatalf("missing delete result summary in %q", tt.Texts())
	}
}

func TestWP4ManualSavepointAndSmartFullCycle(t *testing.T) {
	a, tt := world(t)
	_, _ = a.svc.KeyInit(testPass)
	_ = a.svc.BackUpKeyLater()
	ref := identity.AgentRef{ServerUID: "srv-1", InstanceID: "openclaw", Generation: "g1"}
	a.agents.value.Items = []service.AgentSummary{
		{
			ConnectionID: "box",
			AgentRef:     &ref,
			Instance:     enroll.InstanceSummary{InstanceID: "openclaw", ProfileID: "openclaw", Label: "Main"},
		},
		{
			ConnectionID: "box",
			Instance:     enroll.InstanceSummary{InstanceID: "openclaw-sub", ProfileID: "openclaw", Name: "sub", Label: "Sub"},
		},
	}
	ps := &protState{
		ref: &ref,
		view: &protection.View{
			Mode: "plan",
			ServerPlan: &protection.ServerPlan{
				Revision: 1,
				Desired:  protection.NewDefault(),
			},
		},
	}
	a.prots["box/openclaw"] = ps

	var gotReq service.ManualSavepointRequest
	a.createManualSavepointFn = func(_ context.Context, req service.ManualSavepointRequest) (*service.ManualSavepointResult, error) {
		gotReq = req
		return &service.ManualSavepointResult{
			Host:     req.Host,
			Tier:     req.Tier,
			BundleID: "b_manual",
			Members:  req.BundleMembers,
			Reports:  []vaultrpc.SealReport{{SnapshotID: "s_m1"}},
			Syncs:    []service.ManualSavepointSyncResult{{Target: service.ManualSavepointTarget{Kind: service.SnapshotLocationLocalRepo, Name: "disk"}, OK: true}},
		}, nil
	}

	a.openAgent("box", "openclaw")
	a.agentTab = tabManage
	tt.Advance(time.Second)
	tt.Frame()
	if !tt.HasText(T("manual.title")) || !tt.HasText(T("protect.fullcycle.label")) {
		t.Fatalf("missing WP4 manual/fullcycle UI in %q", tt.Texts())
	}

	// Check same-harness peer and submit manual savepoint.
	ps.manualState().tier = "soul-only"
	ps.manualState().members["openclaw-sub"] = true
	tt.Frame()
	if err := tt.Click(T("manual.create")); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if gotReq.Tier != "soul-only" || len(gotReq.BundleMembers) != 2 {
		t.Fatalf("unexpected manual savepoint request: %#v", gotReq)
	}
	if !tt.HasText(T("manual.result_ok", "n", 1)) {
		t.Fatalf("missing manual savepoint result in %q", tt.Texts())
	}

	// Verify FullCycle and BundleMembers in schedule builder.
	se := newSchedEdit(ps.desired())
	se.cadence = "daily"
	se.at = "03:30"
	se.tz = "UTC"
	se.fullCycle = protection.FullCycleWeekly
	se.bundleMembers["openclaw-sub"] = true
	sched, ok := buildScheduleFromEdit(se, protection.Schedule{})
	if !ok || sched.FullCycle != protection.FullCycleWeekly {
		t.Fatalf("expected weekly fullCycle, got ok=%v sched=%#v", ok, sched)
	}
	members := buildBundleMembersFromEdit("openclaw", se.bundleMembers)
	if len(members) != 2 || members[0] != "openclaw" || members[1] != "openclaw-sub" {
		t.Fatalf("unexpected bundle members: %v", members)
	}
}

func TestWP6GroupedHomeSubAgentsAndBundleIntercept(t *testing.T) {
	a, tt := world(t)
	a.agents.value.Items = []service.AgentSummary{
		{
			ConnectionID: "box",
			Instance: enroll.InstanceSummary{
				InstanceID: "openclaw",
				ProfileID:  "openclaw",
				Label:      "MainBot",
				Service:    []enroll.ServiceState{{Unit: "openclaw.service", Active: "active"}},
			},
		},
		{
			ConnectionID: "box",
			Instance: enroll.InstanceSummary{
				InstanceID: "openclaw-worker",
				ProfileID:  "openclaw",
				Name:       "worker",
				Label:      "WorkerBot",
				Service:    []enroll.ServiceState{{Unit: "worker.service", Active: "failed"}},
			},
		},
	}

	// Flat is default; toggle to Grouped and verify persistence & sub-agent trouble propagation.
	a.router.Push("/agents")
	tt.Frame()
	if a.prefs.subAgentGrouped() {
		t.Fatal("expected flat view by default")
	}
	if err := tt.Click(T("home.subview.grouped")); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.prefs.subAgentGrouped() {
		t.Fatal("expected grouped view after toggle")
	}
	if !tt.HasText(T("home.subagents.trouble", "n", 1)) || !tt.HasText(T("home.subagents.count", "n", 1)) {
		t.Fatalf("missing grouped sub-agent trouble/count in %q", tt.Texts())
	}

	// Main agent has SubAgents tab; sub-agent shows parent link.
	a.openAgent("box", "openclaw")
	a.agentTab = tabSubAgents
	tt.Frame()
	if !tt.HasText("WorkerBot") || !tt.HasText(T("subagents.bundle_backup")) {
		t.Fatalf("missing sub-agents tab content in %q", tt.Texts())
	}
	a.openAgent("box", "openclaw-worker")
	tt.Frame()
	if !tt.HasText(T("subagents.parent_link", "name", "MainBot")) {
		t.Fatalf("missing parent main agent link in %q", tt.Texts())
	}

	// BundleNotStandaloneError interception and switch to BundleRestore.
	a.prepareRestoreFn = func(context.Context, service.RestoreRequest) (*service.PreparedRestore, error) {
		return nil, &service.BundleNotStandaloneError{
			UserErr:  valise.New("bundle_not_standalone", "bundled", "use bundle"),
			BundleID: "b_01",
			Members:  []string{"openclaw", "openclaw-worker"},
			Others:   []string{"openclaw-worker"},
		}
	}
	var bundlePrepCalled, bundleRunCalled bool
	a.prepareBundleRestoreFn = func(_ context.Context, opts service.BundleRestoreOptions, _ string) (*service.BundleRestorePlan, error) {
		bundlePrepCalled = true
		return &service.BundleRestorePlan{
			BundleID: opts.BundleID,
			From:     opts.From,
			Target:   opts.Target,
			Mode:     opts.Mode,
			Order:    []string{"openclaw", "openclaw-worker"},
			Plans: []*service.RevivePlan{
				{Plan: revive.Plan{InstanceID: "openclaw"}},
				{Plan: revive.Plan{InstanceID: "openclaw-worker"}, DataOnly: true},
			},
		}, nil
	}
	a.runBundleRestoreFn = func(_ context.Context, _ *service.BundleRestorePlan, _ service.ReviveConfirm) (*service.BundleRestoreResult, error) {
		bundleRunCalled = true
		return &service.BundleRestoreResult{
			BundleID: "b_01",
			Members:  []service.BundleRestoreMemberResult{{InstanceID: "openclaw"}, {InstanceID: "openclaw-worker"}},
		}, nil
	}

	initTestKey(t, a)
	r := &restoreFlow{
		origin:  "box",
		target:  "box",
		mode:    revive.ModeRollback,
		step:    "setup",
		point:   journey.TimelinePoint{RecordedInstanceID: "openclaw", BundleID: "b_01", BundleMembers: []string{"openclaw", "openclaw-worker"}, SnapshotRef: journey.SnapshotRef{SnapshotID: "s_01"}},
		sources: []service.RestoreSource{{Copy: journey.Copy{LocationRef: journey.LocationRef{Kind: "server_vault", ID: "box"}}}},
	}
	a.restore = r
	a.prepareRestore(r)
	a.router.Push("/restore")
	tt.Frame()
	if !tt.HasText(T("bundle.intercept.msg", "members", "openclaw-worker")) {
		t.Fatalf("missing bundle intercept warning in %q", tt.Texts())
	}
	if err := tt.Click(T("bundle.switch_restore")); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click(T("bundle.restore.prepare")); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !bundlePrepCalled || !tt.HasText(T("bundle.member.sub_skip_service")) {
		t.Fatalf("bundle restore plan missing: prep=%v texts=%q", bundlePrepCalled, tt.Texts())
	}
	if err := tt.Click(T("bundle.restore.confirm_check")); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click(T("bundle.restore.run")); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !bundleRunCalled || !tt.HasText(T("bundle.restore.result_ok")) {
		t.Fatalf("bundle restore run failed: run=%v texts=%q", bundleRunCalled, tt.Texts())
	}
}

func TestWP6MoveAllReadinessAndPartialFailure(t *testing.T) {
	a, tt := world(t)
	initTestKey(t, a)
	a.agents.value.Items = nil
	a.agents.value.Observations = []service.AgentServerObservation{{ConnectionID: "box", Status: "fresh"}, {ConnectionID: "box2", Status: "fresh"}}
	a.inspectHarnessMoveFn = func(_ context.Context, from, target string) (*service.HarnessMoveInspection, error) {
		return &service.HarnessMoveInspection{
			From:   from,
			Target: target,
			Groups: []service.HarnessMoveGroup{
				{
					ProfileID: "openclaw",
					Ready:     true,
					Members:   []service.HarnessMoveMember{{InstanceID: "openclaw", Ready: true, Snapshot: &vault.Meta{Tier: vault.TierFull}}},
				},
				{
					ProfileID: "hermes",
					Ready:     false,
					Reason:    "member hermes lacks full backup",
					Members:   []service.HarnessMoveMember{{InstanceID: "hermes", Ready: false, Reason: "no full backup"}},
				},
			},
		}, nil
	}
	a.prepareHarnessMoveFn = func(_ context.Context, o service.HarnessMoveOptions, _ string) (*service.HarnessMovePlan, error) {
		return &service.HarnessMovePlan{
			From:   o.From,
			Target: o.Target,
			Groups: []service.HarnessMoveGroupPlan{
				{ProfileID: "openclaw", Group: service.HarnessMoveGroup{ProfileID: "openclaw", Ready: true, Members: []service.HarnessMoveMember{{InstanceID: "openclaw"}}}},
			},
		}, nil
	}
	a.runHarnessMoveFn = func(_ context.Context, _ *service.HarnessMovePlan, _ service.ReviveConfirm) (*service.HarnessMoveResult, error) {
		return &service.HarnessMoveResult{
			From:    "box",
			Target:  "box2",
			Partial: true,
			Groups: []service.HarnessMoveGroupResult{
				{ProfileID: "openclaw", Succeeded: true, Members: []service.BundleRestoreMemberResult{{InstanceID: "openclaw"}}},
				{ProfileID: "hermes", Succeeded: false, Error: "target busy"},
			},
		}, nil
	}

	a.router.Push("/servers/box/move-all")
	tt.Frame()
	if err := tt.Click(T("moveall.inspect")); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText(T("moveall.group.ready")) || !tt.HasText(T("moveall.group.not_ready")) || !tt.HasText("member hermes lacks full backup") {
		t.Fatalf("missing move-all readiness in %q", tt.Texts())
	}
	if err := tt.Click(T("moveall.prepare", "n", 1)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click(T("moveall.confirm_check")); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click(T("moveall.run")); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText(T("moveall.result_partial")) || !tt.HasText(T("moveall.partial_hint")) {
		t.Fatalf("missing partial failure summary in %q", tt.Texts())
	}
}

func TestGUIFilesUnder400Lines(t *testing.T) {
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
			t.Errorf("%s has %d lines (limit 400)", f, lines)
		}
	}
}
