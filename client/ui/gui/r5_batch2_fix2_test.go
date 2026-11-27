package gui

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"valise/client/config"
	"valise/core/agentkit"
	"valise/core/enroll"
	"valise/core/identity"
	"valise/core/journey"
	"valise/core/protection"
	"valise/core/vault"
	"valise/core/vaultrpc"
	"valise/ui/service"
)

func TestJourneyDeleteCloudAndLegacyTargetMapping(t *testing.T) {
	a, tt := world(t)
	a.svc.Config.Clouds = []config.Cloud{{Name: "local-gdrive"}}

	// 1. deleteLocationsFromPoints maps "cloud" and "legacy_target".
	pts := []journey.TimelinePoint{
		{
			SnapshotRef:        journey.SnapshotRef{SnapshotID: "s_01"},
			RecordedInstanceID: "demo",
			Copies: []journey.Copy{
				{LocationRef: journey.LocationRef{Kind: "server_vault", ID: "box"}, SourceLocator: "box/demo/2026-11-01_2026-11-30"},
				{LocationRef: journey.LocationRef{Kind: "cloud", ID: "local-gdrive"}, SourceLocator: "box/demo/2026-11-01_2026-11-30"},
				{LocationRef: journey.LocationRef{Kind: "legacy_target", ID: "nas"}, SourceLocator: "box/demo/2026-11-01_2026-11-30"},
			},
		},
	}
	locs := deleteLocationsFromPoints(pts, map[string]bool{"s_01": true})
	wantKinds := map[string]string{
		service.SnapshotLocationServerVault:  "",
		service.SnapshotLocationLocalCloud:   "local-gdrive",
		service.SnapshotLocationServerTarget: "nas",
	}
	for k, name := range wantKinds {
		found := false
		for _, l := range locs {
			if l.Kind == k && l.Name == name {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing mapped location %s:%s in %+v", k, name, locs)
		}
	}

	// 2. openLocationSnapDelete distinguishes local cloud vs server-only cloud.
	a.openLocationSnapDelete(location{Name: "local-gdrive", Kind: locCloud}, "box", periodRow{leaf: "demo/savepoints", here: []string{"s_01"}})
	if len(a.snapDelete.locations) != 1 || a.snapDelete.locations[0].Kind != service.SnapshotLocationLocalCloud {
		t.Fatalf("expected SnapshotLocationLocalCloud for local cloud, got %+v", a.snapDelete.locations)
	}
	a.openLocationSnapDelete(location{Name: "server-rclone", Kind: locCloud}, "box", periodRow{leaf: "demo/savepoints", here: []string{"s_01"}})
	if len(a.snapDelete.locations) != 1 || a.snapDelete.locations[0].Kind != service.SnapshotLocationServerTarget {
		t.Fatalf("expected SnapshotLocationServerTarget for server-only cloud, got %+v", a.snapDelete.locations)
	}

	// 3. pointLeaf never fabricates invalid YYYY-MM period fallback.
	badPt := journey.TimelinePoint{
		SnapshotRef:        journey.SnapshotRef{SnapshotID: "s_bad"},
		RecordedInstanceID: "demo",
		CreatedAt:          time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC),
		Copies:             []journey.Copy{{LocationRef: journey.LocationRef{Kind: "server_vault", ID: "box"}, SourceLocator: "box/-"}},
	}
	if lf := pointLeaf(badPt); lf != "" {
		t.Fatalf("expected empty leaf for non-savepoint without valid locator, got %q", lf)
	}
	spPt := badPt
	spPt.Kind = vault.KindSavepoint
	if lf := pointLeaf(spPt); lf != "demo/savepoints" {
		t.Fatalf("expected savepoints leaf, got %q", lf)
	}
	a.timeline.value = service.JourneyPage{Items: []journey.TimelinePoint{badPt}}
	a.openSinglePointDelete(&agentRow{Server: "box", Instance: "demo"}, badPt)
	a.openAgent("box", "demo")
	a.agentTab = tabJourney
	tt.Advance(time.Second)
	tt.Frame()
	if !tt.HasText(T("snapdel.invalid_leaf")) {
		t.Fatalf("expected invalid leaf notice in %q", tt.Texts())
	}

	// 4. runSnapDeleteExecute refreshes locations state (clears cached lists).
	ls := a.locs()
	ls.lists["server-rclone/box"] = &listRead{list: &vaultrpc.TargetListResult{}}
	a.openLocationSnapDelete(location{Name: "server-rclone", Kind: locCloud}, "box", periodRow{leaf: "demo/savepoints", here: []string{"s_01"}})
	a.snapDelete.selected["s_01"] = true
	a.snapDelete.checkedLoc[deleteLocKey(a.snapDelete.locations[0])] = true
	a.deleteSnapshotsFn = func(_ context.Context, req service.SnapshotDeleteRequest) (*service.SnapshotDeleteBatchResult, error) {
		return &service.SnapshotDeleteBatchResult{Host: req.Host, Leaf: req.Leaf}, nil
	}
	a.runSnapDeleteExecute(a.snapDelete)
	if _, stale := ls.lists["server-rclone/box"]; stale {
		t.Fatalf("expected location list cache to be cleared by readLocations() after delete")
	}
}

func TestTabManageSinglePrimaryButtonAndManualTargetsDedup(t *testing.T) {
	// 1. Verify agentkit.go no longer uses PrimaryButton for kit.install_open.
	src, err := os.ReadFile("agentkit.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), `ui.PrimaryButton(c, T("kit.install_open"))`) {
		t.Fatal("agentkit.go must not use PrimaryButton for kit.install_open (tabManage single PrimaryButton rule)")
	}

	a, tt := world(t)
	a.agentKitHook = &agentKitOps{
		status: func(context.Context, string, string) (*agentkit.KitStatusResult, error) {
			return &agentkit.KitStatusResult{
				Dir:    "/home/me/.skills",
				Skills: []agentkit.SkillState{{Skill: "valise-status", State: agentkit.SkillMissing}},
			}, nil
		},
		guide: func(context.Context, string, string) (*service.KitGuide, error) {
			return &service.KitGuide{}, nil
		},
	}
	ref := identity.AgentRef{ServerUID: "srv-1", InstanceID: "demo", Generation: "g1"}
	a.agents.value.Items = []service.AgentSummary{
		{ConnectionID: "box", AgentRef: &ref, Instance: enroll.InstanceSummary{InstanceID: "demo", ProfileID: "demo", Label: "Demo"}},
	}
	if err := config.SaveStations(a.svc.Dirs.Data, []config.Station{{Name: "offsite", User: "u", Address: "10.0.0.2", Path: "/srv/v"}}); err != nil {
		t.Fatal(err)
	}
	a.svc.Config.Repos = []config.Repo{{Name: "disk", Path: t.TempDir()}}
	a.svc.Config.Clouds = []config.Cloud{{Name: "gdrive"}}

	// Ensure a.loc is nil (user never opened Locations page), while ps.serverTargets
	// and ServerCopies both reference the station "offsite" plus non-station "nas".
	a.loc = nil
	desired := protection.NewDefault()
	desired.ServerCopies = []protection.ServerCopy{
		{TargetRef: protection.LocationRef{Kind: "target", ID: "offsite"}},
		{TargetRef: protection.LocationRef{Kind: "target", ID: "nas"}},
	}
	ps := &protState{
		ref: &ref,
		view: &protection.View{
			Mode:       "plan",
			ServerPlan: &protection.ServerPlan{Revision: 1, Desired: desired},
		},
		serverTargets: []vaultrpc.TargetState{
			{Target: vaultrpc.Target{Name: "offsite"}},
			{Target: vaultrpc.Target{Name: "nas"}},
			{Target: vaultrpc.Target{Name: "extra-ssh"}},
		},
	}
	a.prots["box/demo"] = ps

	// 2. Verify manualTargetOptions works with a.loc == nil and never duplicates station.
	row := &agentRow{Server: "box", Instance: "demo", Profile: "demo"}
	opts := a.manualTargetOptions(row, ps)
	offsiteCount := 0
	hasExtraSSH := false
	for _, opt := range opts {
		if opt.Name == "offsite" {
			offsiteCount++
			if opt.Kind != service.SnapshotLocationStation {
				t.Fatalf("expected station kind for offsite, got %+v", opt)
			}
		}
		if opt.Name == "extra-ssh" && opt.Kind == service.SnapshotLocationServerTarget {
			hasExtraSSH = true
		}
	}
	if offsiteCount != 1 {
		t.Fatalf("expected exactly 1 deduplicated entry for station offsite, got %d in %+v", offsiteCount, opts)
	}
	if !hasExtraSSH {
		t.Fatalf("expected server target extra-ssh from ps.serverTargets even when a.loc == nil: %+v", opts)
	}

	// 3. Verify manual savepoint enforces needKey gate before CreateManualSavepoint.
	called := false
	a.createManualSavepointFn = func(_ context.Context, req service.ManualSavepointRequest) (*service.ManualSavepointResult, error) {
		called = true
		return &service.ManualSavepointResult{Host: req.Host, Tier: req.Tier, Reports: []vaultrpc.SealReport{{SnapshotID: "s_1"}}}, nil
	}
	a.openAgent("box", "demo")
	a.agentTab = tabManage
	tt.Advance(time.Second)
	tt.Frame()
	if err := tt.Click(T("manual.create")); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if called {
		t.Fatal("CreateManualSavepoint must not run before needKey gate completes")
	}
	if a.keyFlow == nil || a.keyFlow.step != kfSet {
		t.Fatalf("expected needKey dialog (kfSet), got %+v", a.keyFlow)
	}

	// After key is initialized and backup decided, clicking manual.create succeeds.
	a.keyFlow = nil
	if _, err := a.svc.KeyInit(testPass); err != nil {
		t.Fatal(err)
	}
	if err := a.svc.BackUpKeyLater(); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click(T("manual.create")); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !called {
		t.Fatal("expected CreateManualSavepoint to run once key gate is satisfied")
	}
}
