package revive

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"valise/core/environment"
	"valise/core/identity"
)

func activationRecordFixture(t *testing.T) (*managedWorld, *ActivationRecord) {
	t.Helper()
	w := newManagedWorld(t, false)
	w.begin(t)
	w.transition(t, environment.Registry.Prepared)
	j, _ := w.r.Store.Load(w.p.OpID)
	rec := &ActivationRecord{Schema: activationSchema, State: "running", OriginalDefinition: strings.Repeat("c", 64), CandidateDefinition: strings.Repeat("d", 64),
		Plan: ActivationPlan{Agent: w.scope.Agent, OperationID: w.scope.OperationID, Snapshot: w.scope.Snapshot, Original: w.scope.Original, Candidate: w.scope.Candidate,
			DataOpID: w.p.OpID, OriginalMaterials: strings.Repeat("a", 64), CandidateMaterials: strings.Repeat("b", 64), OriginalProfile: w.p.Profile, CandidateProfile: w.p.Profile},
		DataPlanDigest: j.ManagedData.PlanDigest, DataManifestDigest: j.ManagedData.ManifestDigest,
		Facts: map[string]ActivationFact{"disable_original": {IntentAt: time.Now().UTC()}}}
	rec.BindingDigest = activationDigest(rec)
	if err := saveActivation(w.home, rec); err != nil {
		t.Fatal(err)
	}
	return w, rec
}

func TestActivationQueriesAndRepeatedExecutionNeverReplayIntent(t *testing.T) {
	w, rec := activationRecordFixture(t)
	called := 0
	a := NewActivationRunner(w.r, func(context.Context, identity.AgentRef, environment.Ref, string) error { called++; return nil })
	dir, _ := activationDir(w.home, rec.Plan.Agent, false)
	path := filepath.Join(dir, rec.Plan.OperationID+".json")
	before, _ := os.ReadFile(path)
	for range 2 {
		review, err := a.Query(context.Background(), rec.Plan.Agent, rec.Plan.OperationID)
		if err != nil || review.Record.State != "running" || review.Current != nil || review.Phase != environment.Prepared || review.IssueCode != "activation_runtime_unknown" {
			t.Fatal(review, err)
		}
		review, err = a.Activate(context.Background(), rec.Plan.Agent, rec.Plan.OperationID)
		if err != nil || review.Record.State != "running" {
			t.Fatal(review, err)
		}
	}
	// Reopening an interrupted transaction does not need a material provider.
	if _, err := NewActivationRunner(w.r, nil).Activate(context.Background(), rec.Plan.Agent, rec.Plan.OperationID); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if called != 0 || string(before) != string(after) || read(t, filepath.Join(w.state, "SOUL.md")) != "current soul" {
		t.Fatal("query replayed or rewrote an unfinished operation")
	}
	if _, err := os.Stat(filepath.Join(w.home, "unit-state")); !os.IsNotExist(err) {
		t.Fatal("query invoked the service fixture")
	}
}

func TestMissingActivationQueryDoesNotInitialize(t *testing.T) {
	home := t.TempDir()
	a := NewActivationRunner(&Runner{Store: Store{Home: home}}, nil)
	agent := identity.AgentRef{ServerUID: "server", InstallID: "install", InstanceID: "agent", Generation: "generation"}
	if _, err := a.Query(context.Background(), agent, strings.Repeat("a", 32)); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(home)
	if len(entries) != 0 {
		t.Fatal("missing query created private state")
	}
}

func TestActivationStoreRejectsCorruptionExposureAndAliases(t *testing.T) {
	for _, change := range []string{"binding", "unknown", "trailing", "symlink", "hardlink", "permissions"} {
		t.Run(change, func(t *testing.T) {
			w, rec := activationRecordFixture(t)
			dir, _ := activationDir(w.home, rec.Plan.Agent, false)
			path := filepath.Join(dir, rec.Plan.OperationID+".json")
			var err error
			switch change {
			case "binding":
				rec.Plan.DataOpID += "changed"
				b, _ := json.Marshal(rec)
				err = os.WriteFile(path, b, 0600)
			case "unknown", "trailing":
				b, _ := os.ReadFile(path)
				if change == "unknown" {
					b = append([]byte(`{"unrecognized":1,`), b[1:]...)
				} else {
					b = append(b, []byte(` {}`)...)
				}
				err = os.WriteFile(path, b, 0600)
			case "symlink":
				err = os.Rename(path, path+".original")
				if err == nil {
					err = os.Symlink(path+".original", path)
				}
			case "hardlink":
				err = os.Link(path, filepath.Join(t.TempDir(), "alias"))
			case "permissions":
				err = os.Chmod(path, 0644)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := loadActivation(w.home, rec.Plan.Agent, rec.Plan.OperationID); err == nil {
				t.Fatal("bad activation accepted", change)
			}
		})
	}
}

func TestActivationRequiresUnchangedChildBindingAndMaterialProvider(t *testing.T) {
	w, rec := activationRecordFixture(t)
	rec.State = "ready"
	if err := saveActivation(w.home, rec); err != nil {
		t.Fatal(err)
	}
	if _, err := NewActivationRunner(w.r, nil).Activate(context.Background(), rec.Plan.Agent, rec.Plan.OperationID); err == nil {
		t.Fatal("activated without private material verifier")
	}
	j, _ := w.r.Store.Load(w.p.OpID)
	j.ManagedData.ManifestDigest = strings.Repeat("f", 64)
	if err := w.r.Store.Save(j); err != nil {
		t.Fatal(err)
	}
	called := false
	a := NewActivationRunner(w.r, func(context.Context, identity.AgentRef, environment.Ref, string) error { called = true; return nil })
	if _, err := a.Activate(context.Background(), rec.Plan.Agent, rec.Plan.OperationID); err == nil || called {
		t.Fatal("changed child binding reached activation", err)
	}
	if read(t, filepath.Join(w.state, "SOUL.md")) != "current soul" {
		t.Fatal("failed binding check changed active data")
	}
}
