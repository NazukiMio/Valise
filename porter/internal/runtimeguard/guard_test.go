package runtimeguard

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"valise/core/enroll"
	"valise/core/environment"
	"valise/core/identity"
	"valise/core/profile"
	"valise/core/revive"
	"valise/core/vault"
)

func put(t *testing.T, home, rel string, value any) string {
	t.Helper()
	path := filepath.Join(home, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func fixture(t *testing.T) (string, preparation, string) {
	t.Helper()
	home := t.TempDir()
	if err := os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	p := preparation{Schema: "valise-environment-preparation/1", State: "capturing", OperationID: strings.Repeat("a", 32)}
	p.Params.Agent = identity.AgentRef{ServerUID: "server", InstallID: "demo", InstanceID: "demo", Generation: "generation"}
	p.Plan = revive.Plan{InstanceID: "demo", OpID: vault.NewID("op", time.Now()), Roots: []revive.RootPlan{{ID: "state", Path: filepath.Join(home, "state")}}}
	p.Native.Unit, p.Native.Executable = "demo.service", filepath.Join(home, "runtime/node")
	p.Params.Input.Header.Runtime = &vault.RuntimeRecord{Dependencies: []vault.RuntimeDependency{{Path: filepath.Join(home, "programme")}}}
	d := enroll.Dossier{InstallID: p.Params.Agent.InstallID}
	d.Put(enroll.Record{InstanceID: p.Params.Agent.InstanceID, ProfileID: p.Params.Agent.InstallID, Roots: map[string]string{"state": p.Plan.Roots[0].Path}}, time.Now())
	put(t, home, ".config/valise/dossiers/demo.json", d)
	put(t, home, ".config/valise/dossiers/demo.profile.json", profile.Profile{ID: "demo", ProfileVersion: 1})
	rel := filepath.Join(stateDir, "environment-preparations", environment.Key(p.Params.Agent), p.OperationID+".json")
	put(t, home, rel, p)
	return home, p, rel
}

func TestPreparationOwnershipUsesActualResources(t *testing.T) {
	home, p, _ := fixture(t)
	key := environment.Key(p.Params.Agent)
	for name, scope := range map[string]Scope{
		"same instance":                   {Instances: []string{"demo"}},
		"other instance overlapping root": {Instances: []string{"other"}, Paths: []string{filepath.Join(home, "state/child")}},
		"parent of source":                {Paths: []string{home}},
		"source programme":                {Paths: []string{filepath.Join(home, "programme/new")}},
		"source interpreter":              {Paths: []string{p.Native.Executable}},
		"shared service":                  {Units: []profile.ServiceUnit{{Scope: "user", Unit: p.Native.Unit}}},
		"managed service":                 {Units: []profile.ServiceUnit{{Scope: "user", Unit: "valise-agent-" + key + ".service"}}},
		"private programme":               {Paths: []string{filepath.Join(home, stateDir, "programs", key, "B", "1")}},
		"child data stage":                {Paths: []string{filepath.Join(home, ".valise-stage-"+p.Plan.OpID+"-state")}},
		"unscoped exec":                   {All: true},
	} {
		t.Run(name, func(t *testing.T) {
			if err := Check(context.Background(), home, scope); err == nil || !strings.Contains(err.Error(), "environment_in_use") {
				t.Fatal("occupied resource allowed", err)
			}
		})
	}
	for name, scope := range map[string]Scope{
		"other instance":          {Instances: []string{"other"}, Paths: []string{filepath.Join(home, "other-state")}, Units: []profile.ServiceUnit{{Scope: "user", Unit: "other.service"}}},
		"different service scope": {Units: []profile.ServiceUnit{{Scope: "system", Unit: p.Native.Unit}}},
		"similar path prefix":     {Paths: []string{filepath.Join(home, "state-other")}},
		"read only":               {},
	} {
		t.Run(name, func(t *testing.T) {
			if err := Check(context.Background(), home, scope); err != nil {
				t.Fatal("unrelated scope blocked", err)
			}
		})
	}
	if err := Check(context.Background(), t.TempDir(), Scope{Instances: []string{"demo"}, All: true}); err != nil {
		t.Fatal("another server affected", err)
	}
}

func TestAliasesAndCancelledMaterialsStayProtected(t *testing.T) {
	home, p, rel := fixture(t)
	if err := os.Mkdir(filepath.Join(home, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, "state"), filepath.Join(home, "alias")); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"capturing", "cancelled", "cleaning"} {
		p.State = state
		put(t, home, rel, p)
		if err := Check(context.Background(), home, Scope{Paths: []string{filepath.Join(home, "alias/not-yet-created/child")}}); err == nil {
			t.Fatal("path alias bypassed", state)
		}
	}
	p.State = "cleaned"
	put(t, home, rel, p)
	if err := Check(context.Background(), home, Scope{All: true}); err != nil {
		t.Fatal("cleaned tombstone retained ownership", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Check(ctx, home, Scope{}); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled read continued", err)
	}
}

func TestRegistryAndInstanceLookupsDoNotMigrate(t *testing.T) {
	home := t.TempDir()
	if err := os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	a := identity.AgentRef{ServerUID: "server", InstallID: "demo", InstanceID: "demo", Generation: "generation"}
	r, err := environment.New(a)
	if err != nil {
		t.Fatal(err)
	}
	d := enroll.Dossier{InstallID: "demo"}
	d.Put(enroll.Record{InstanceID: "demo", ProfileID: "demo", Roots: map[string]string{"state": filepath.Join(home, "state")}}, time.Now())
	put(t, home, ".config/valise/dossiers/demo.json", d)
	put(t, home, ".config/valise/dossiers/demo.profile.json", profile.Profile{ID: "demo"})
	put(t, home, filepath.Join(stateDir, "environments", environment.Key(a)+".json"), r)
	// No identity file exists. Reading the committed dossier must suffice,
	// without manufacturing an identity or importing legacy registrations.
	if err := CheckInstance(context.Background(), home, "demo"); err == nil {
		t.Fatal("registry alone did not protect its instance")
	}
	put(t, home, ".config/valise/instances/other.json", enroll.Record{InstanceID: "other", ProfileID: "other", Roots: map[string]string{"state": filepath.Join(home, "state/child")}})
	if err := CheckInstance(context.Background(), home, "other"); err == nil {
		t.Fatal("legacy overlapping instance allowed")
	}
	if _, err := os.Stat(filepath.Join(home, ".config/valise/identity")); !os.IsNotExist(err) {
		t.Fatal("read initialized identity", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".config/valise/instances/other.json")); err != nil {
		t.Fatal("read moved legacy record", err)
	}
}

func TestUnsafeOwnershipRefusesWithoutExposingContents(t *testing.T) {
	for _, change := range []string{"permissions", "symlink", "hardlink", "bad-json"} {
		t.Run(change, func(t *testing.T) {
			home, _, rel := fixture(t)
			path := filepath.Join(home, rel)
			var err error
			switch change {
			case "permissions":
				err = os.Chmod(path, 0644)
			case "symlink":
				err = os.Rename(path, path+".saved")
				if err == nil {
					err = os.Symlink(path+".saved", path)
				}
			case "hardlink":
				err = os.Link(path, path+".link")
			case "bad-json":
				err = os.WriteFile(path, []byte("fixture-private-content"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			err = Check(context.Background(), home, Scope{Paths: []string{filepath.Join(home, "other")}})
			if err == nil || !strings.Contains(err.Error(), "environment_ownership_unknown") || strings.Contains(err.Error(), "fixture-private-content") {
				t.Fatal("unsafe ownership accepted or exposed", err)
			}
		})
	}
	home := t.TempDir()
	if err := Check(context.Background(), home, Scope{All: true}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatal("empty read initialized state", entries, err)
	}
}

func TestDisjointNamedAgentCannotRewriteSharedInstallationProfile(t *testing.T) {
	home, p, _ := fixture(t)
	accepted := profile.Profile{ID: p.Params.Agent.InstallID, ProfileVersion: 1}
	put(t, home, ".config/valise/dossiers/demo.profile.json", accepted)
	rec := enroll.Record{InstanceID: "demo-other", Name: "other", ProfileID: accepted.ID, Roots: map[string]string{"state": filepath.Join(home, "other-state")}}
	if err := CheckRecord(context.Background(), home, rec, &accepted); err != nil {
		t.Fatal("unchanged shared profile blocked a disjoint named Agent", err)
	}
	changed := accepted
	changed.ProfileVersion++
	if err := CheckRecord(context.Background(), home, rec, &changed); err == nil || !strings.Contains(err.Error(), "environment_in_use") {
		t.Fatal("disjoint instance rewrote occupied installation profile", err)
	}
}

func TestAbsentOwnershipReadDoesNotCreateState(t *testing.T) {
	home := t.TempDir()
	if err := os.Chmod(home, 0775); err != nil {
		t.Fatal(err)
	}
	if _, err := load(home); err != nil {
		t.Fatal("unmanaged HOME inherited private environment constraints", err)
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatal("read wrote state", err)
	}
}

func TestOwnershipUsesProfileRootIDGrammar(t *testing.T) {
	home, p, rel := fixture(t)
	p.Plan.Roots[0].ID = "1-data"
	put(t, home, rel, p)
	err := Check(context.Background(), home, Scope{Paths: []string{p.Plan.Roots[0].Path}})
	if err == nil || !strings.Contains(err.Error(), "environment_in_use") {
		t.Fatal("valid profile root could not be checked", err)
	}
}
