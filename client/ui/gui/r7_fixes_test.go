package gui

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/profile"
	"valise/core/revive"
	"valise/core/valise"
	"valise/ui/service"
)

func initTestKey(t *testing.T, a *App) {
	t.Helper()
	if _, err := a.svc.KeyInit(testPass); err != nil {
		t.Fatal(err)
	}
	a.svc.BackUpKeyLater()
}

func TestR7NeedKeyGatesBundleAndMoveAll(t *testing.T) {
	a, _ := world(t)
	tt := ui.NewTester(a.View, 1180, 1800)
	a.agents.value.Observations = []service.AgentServerObservation{
		{ConnectionID: "box", Status: "fresh"},
		{ConnectionID: "box2", Status: "fresh"},
	}

	// 1. Bundle restore prepare button must gate on needKey when no key exists.
	bundlePreps := 0
	a.prepareBundleRestoreFn = func(_ context.Context, opts service.BundleRestoreOptions, _ string) (*service.BundleRestorePlan, error) {
		bundlePreps++
		return &service.BundleRestorePlan{
			BundleID: "b1", From: opts.From, Target: opts.Target, Mode: opts.Mode,
			Order: []string{"inst-a"},
			Plans: []*service.RevivePlan{{Target: opts.Target, From: opts.From, Plan: revive.Plan{InstanceID: "inst-a"}}},
		}, nil
	}
	a.bundleRestore = &bundleRestoreFlow{from: "box", target: "box2", mode: revive.ModeMigrate, bundleID: "b1"}
	a.router.Push("/restore")
	tt.Frame()
	click(t, tt, T("bundle.restore.prepare"))
	if bundlePreps != 0 {
		t.Fatalf("bundle prepare ran without key: %d", bundlePreps)
	}
	if a.keyFlow == nil || a.keyFlow.step != kfSet {
		t.Fatalf("expected keyFlow(kfSet) on bundle prepare without key, got %+v", a.keyFlow)
	}
	a.keyFlow = nil
	a.bundleRestore = nil

	// 2. Move-all prepare button must gate on needKey when no key exists.
	movePreps := 0
	a.prepareHarnessMoveFn = func(_ context.Context, o service.HarnessMoveOptions, _ string) (*service.HarnessMovePlan, error) {
		movePreps++
		return &service.HarnessMovePlan{From: o.From, Target: o.Target}, nil
	}
	st := a.moveAllFor("box")
	st.target = "box2"
	st.inspect = &service.HarnessMoveInspection{
		From: "box", Target: "box2",
		Groups: []service.HarnessMoveGroup{{
			ProfileID: "demo-harness", Ready: true,
			Members: []service.HarnessMoveMember{{InstanceID: "inst-m", Ready: true}},
		}},
	}
	st.selected = map[string]bool{"demo-harness": true}
	a.router.Push("/servers/box/move-all")
	tt.Frame()
	click(t, tt, T("moveall.prepare", "n", 1))
	if movePreps != 0 {
		t.Fatalf("move-all prepare ran without key: %d", movePreps)
	}
	if a.keyFlow == nil || a.keyFlow.step != kfSet {
		t.Fatalf("expected keyFlow(kfSet) on move-all prepare without key, got %+v", a.keyFlow)
	}

	// 3. Once key is initialized and backup decision made, prepare proceeds.
	a.keyFlow = nil
	if _, err := a.svc.KeyInit(testPass); err != nil {
		t.Fatal(err)
	}
	a.svc.BackUpKeyLater()
	tt.Frame()
	click(t, tt, T("moveall.prepare", "n", 1))
	if movePreps != 1 {
		t.Fatalf("expected move-all prepare to run once after key ready, got %d", movePreps)
	}
}

func TestR7MoveAllMultipleMissingBodiesSinglePrimaryButton(t *testing.T) {
	a, _ := world(t)
	tt := ui.NewTester(a.View, 1180, 2400)
	a.agents.value.Observations = []service.AgentServerObservation{
		{ConnectionID: "box", Status: "fresh"},
		{ConnectionID: "box2", Status: "fresh"},
	}

	makeBlocked := func(pid, inst string) *service.RevivePlan {
		return &service.RevivePlan{
			Target: "box2", From: "box",
			Plan:     revive.Plan{InstanceID: inst, Profile: profile.Profile{ID: pid}},
			Blockers: []valise.UserError{*valise.New("body_rebuild_unverified", "missing body", "")},
		}
	}
	st := a.moveAllFor("box")
	st.target = "box2"
	st.plan = &service.HarnessMovePlan{
		From: "box", Target: "box2",
		Groups: []service.HarnessMoveGroupPlan{
			{
				ProfileID:  "harness-a",
				Group:      service.HarnessMoveGroup{ProfileID: "harness-a", Ready: true, Members: []service.HarnessMoveMember{{InstanceID: "inst-a"}}},
				SinglePlan: makeBlocked("harness-a", "inst-a"),
			},
			{
				ProfileID:  "harness-b",
				Group:      service.HarnessMoveGroup{ProfileID: "harness-b", Ready: true, Members: []service.HarnessMoveMember{{InstanceID: "inst-b"}}},
				SinglePlan: makeBlocked("harness-b", "inst-b"),
			},
		},
	}
	st.inlineInstalls = map[string]*restoreInstallState{
		"harness-a": {
			host: "box2", profileID: "harness-a",
			run: &customInstallRun{plan: &service.CustomInstallPlan{ID: "p-a", Kind: "script", Command: []string{"sh", "a.sh"}}},
		},
		"harness-b": {
			host: "box2", profileID: "harness-b",
			run: &customInstallRun{plan: &service.CustomInstallPlan{ID: "p-b", Kind: "script", Command: []string{"sh", "b.sh"}}},
		},
	}

	a.router.Push("/servers/box/move-all")
	tt.Advance(time.Second)
	tt.Frame()

	// Both expanded install plans render their run buttons alongside moveall.run.
	hasAll(t, tt.Texts(), T("install.custom.run"), T("moveall.run"))

	// Verify moveall.go enforces single PrimaryButton across multiple expanded plans.
	moveAllSrc, err := os.ReadFile("moveall.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(moveAllSrc), "primaryPlanClaimed") {
		t.Fatal("moveall.go must cap expanded inline install plans to a single PrimaryButton")
	}
	customSrc, err := os.ReadFile("installcustom.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(customSrc), "if allowPrimary {") {
		t.Fatal("installcustom.go customPlanBody must honor allowPrimary")
	}
}
