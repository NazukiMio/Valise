package service

import (
	"strings"
	"testing"

	"valise/core/profile"
	"valise/core/revive"
	"valise/core/vault"
)

// A body inside the state dir that stays (or is reinstalled) travels with
// the swap, only where the target has that root; an older target porter
// may still roll back (the carry copies it) but not migrate over it
// (docs/ARCHITECTURE.md 8.2.1).
func TestNestedBodyPlan(t *testing.T) {
	prof := profile.Profile{ID: "x", DisplayName: "X",
		Paths: []profile.PathRule{{Root: "state", Glob: "config.json", Category: "secrets"}},
		Body:  &profile.Body{Paths: []profile.PathRef{{Root: "state", Glob: "tools/**"}}}}
	head := &vault.Snapshot{}
	for _, exists := range []bool{true, false} {
		rp := &RevivePlan{}
		plan := revive.Plan{Mode: revive.ModeMigrate, Roots: []revive.RootPlan{{ID: "state", Path: "/h/.x"}}}
		facts := revive.InspectResult{Roots: []revive.RootFacts{{ID: "state", Path: "/h/.x", Exists: exists}}}
		planBody(rp, &plan, head, facts, prof, revive.ModeMigrate)
		if plan.Body.Action != revive.BodyKeep || (len(plan.Body.Dirs) == 1) != exists {
			t.Fatalf("exists=%v: %+v", exists, plan.Body)
		}
	}

	for _, tc := range []struct {
		mode    string
		caps    []string
		blocked bool
		dirs    int
	}{
		{revive.ModeMigrate, nil, true, 1},
		{revive.ModeClone, nil, true, 1},
		{revive.ModeRollback, nil, false, 0},
		{revive.ModeMigrate, []string{revive.BodyDirsCapability}, false, 1},
	} {
		rp := &RevivePlan{Target: "t", targetCaps: tc.caps}
		plan := revive.Plan{Mode: tc.mode, Body: revive.BodyPlan{Action: revive.BodyKeep, Dirs: []string{"state/tools"}}}
		planNestedBody(rp, &plan)
		if (len(rp.Blockers) > 0) != tc.blocked || len(plan.Body.Dirs) != tc.dirs {
			t.Fatalf("%s caps=%v: blockers=%+v dirs=%v", tc.mode, tc.caps, rp.Blockers, plan.Body.Dirs)
		}
	}

	facts := revive.InspectResult{Roots: []revive.RootFacts{{ID: "state", Path: "/h/.x", Exists: true}}}
	for _, tc := range []struct {
		mode    string
		secrets bool
		warn    bool
	}{{revive.ModeMigrate, false, true}, {revive.ModeClone, false, true}, {revive.ModeMigrate, true, false}, {revive.ModeRollback, false, false}} {
		rp := &RevivePlan{RestoreSecret: tc.secrets}
		warnCredentialsReplaced(rp, &revive.Plan{Mode: tc.mode}, facts, prof)
		got := strings.Contains(strings.Join(rp.Warnings, "\n"), "这个备份不含凭据")
		if got != tc.warn {
			t.Fatalf("%s secrets=%v: %v", tc.mode, tc.secrets, rp.Warnings)
		}
	}
}
