package revive

import (
	"os"
	"path/filepath"
	"testing"

	"valise/core/environment"
	"valise/core/profile"
	"valise/core/revive"
	"valise/core/rpc"
	"valise/core/vault"
	"valise/porter/internal/environments"
)

func TestPlanningBudgetGroupsNestedDataAndDoesNotCreateDirectories(t *testing.T) {
	home := t.TempDir()
	state := filepath.Join(home, "state")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	roots, err := revive.CanonicalRoots(map[string]string{"state": state, "workspace": filepath.Join(state, "workspace")})
	if err != nil {
		t.Fatal(err)
	}
	program := environments.ProgramMaterial{Files: []environments.ProgramFile{{Path: "bin/gateway", Size: 100}}}
	data := environment.DataPlan{Entries: []environment.DataEntry{{Root: "workspace", Path: "note", File: &vault.File{Size: 80}}}}
	budget, err := planEnvironmentSpace(home, roots, program, data)
	if err != nil || len(budget) != 1 || !budget[0].Programmes || len(budget[0].Roots) != 1 || budget[0].Roots[0] != "state" || budget[0].NeedBytes < 1<<20+400+240 || budget[0].FreeBytes == 0 {
		t.Fatal(budget, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".local")); !os.IsNotExist(err) {
		t.Fatal("space preview initialized state", err)
	}
	data.Entries[0].File.Size = -1
	if _, err := planEnvironmentSpace(home, roots, program, data); err == nil {
		t.Fatal("negative snapshot sizes accepted")
	}
}

func TestPlanningNativeSelectionAndReadOnlyTier(t *testing.T) {
	u := profile.ServiceUnit{Scope: "user", Unit: "fixture.service"}
	p := profile.Profile{Service: &profile.Service{Candidates: []profile.ServiceUnit{u}}}
	if got, err := planningUnit(p); err != nil || got != u {
		t.Fatal(got, err)
	}
	p.Service.Candidates = append(p.Service.Candidates, profile.ServiceUnit{Scope: "user", Unit: "other.service"})
	if _, err := planningUnit(p); err == nil {
		t.Fatal("ambiguous native service guessed")
	}
	p.Service.Candidates = []profile.ServiceUnit{{Scope: "system", Unit: "fixture.service"}}
	if _, err := planningUnit(p); err == nil {
		t.Fatal("system service treated as owned native entry")
	}
	if !rpc.Classified(environment.MethodPlan) || rpc.TierOf(environment.MethodPlan) != rpc.TierDaily {
		t.Fatal("read-only planning tier missing")
	}
}
