package revive

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"valise/core/enroll"
	porterenroll "valise/porter/internal/enroll"

	"valise/core/environment"
	"valise/core/profile"
	"valise/core/revive"
	porteridentity "valise/porter/internal/identity"
	"valise/porter/internal/managedruntime"
)

func preparationFixture(t *testing.T) (*world, *EnvironmentPreparation) {
	t.Helper()
	w, p := environmentWorld(t)
	w.p.Service = &profile.Service{Candidates: []profile.ServiceUnit{{Scope: "user", Unit: "fixture.service"}}}
	p.Profile, p.TestOnly, p.IsolatedTest, p.Body = w.p, false, nil, revive.BodyPlan{Action: revive.BodyKeep}
	// Durable unknown-intent fixture: nothing has been copied or registered.
	ref := managedruntime.NativeRef{Unit: "fixture.service", Definition: strings.Repeat("a", 64), Executable: filepath.Join(w.state, "bin/gateway"), ExecutableSHA256: strings.Repeat("b", 64), MainPID: 2, KernelStart: "1", ControlGroup: fmt.Sprintf("/user.slice/user-%d.slice/user@%d.service/app.slice/fixture.service", os.Getuid(), os.Getuid())}
	binding, err := porteridentity.Read(w.home, "demo")
	if err != nil || binding.Agent == nil {
		t.Fatal(err)
	}
	p0 := revive.EnvironmentPlanParams{Agent: binding.Agent.Ref, Input: environment.TestInput{}}
	rec := &EnvironmentPreparation{Schema: preparationSchema, Mode: environment.Parallel, OperationID: strings.Repeat("c", 32), Params: p0, PreviewDigest: strings.Repeat("d", 64), Plan: p, Native: ref, State: "capturing"}
	rec.Params.Input.Header.SnapshotID = p.SnapshotID
	rec.BindingDigest = preparationDigest(rec)
	return w, rec
}

func TestUnknownPreparationIntentCannotReplayOrClaimAnotherOperation(t *testing.T) {
	w, rec := preparationFixture(t)
	if err := savePreparation(w.home, rec, true); err != nil {
		t.Fatal(err)
	}
	dir, _ := preparationDir(w.home, rec.Params.Agent, false)
	path := filepath.Join(dir, rec.OperationID+".json")
	before, _ := os.ReadFile(path)
	for range 2 {
		got, err := w.r.beginEnvironmentPreparation(context.Background(), rec.Params, rec.PreviewDigest, rec.OperationID, managedruntime.Controller{}, true)
		if err != nil || got.State != "capturing" {
			t.Fatal("unknown intent replayed source inspection", got, err)
		}
		if _, err := w.r.FinishEnvironmentPreparation(context.Background(), rec.Params.Agent, rec.OperationID); err != nil {
			t.Fatal(err)
		}
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("unknown intent rewritten")
	}
	other := *rec
	other.OperationID = strings.Repeat("e", 32)
	other.BindingDigest = preparationDigest(&other)
	if err := savePreparation(w.home, &other, true); err == nil {
		t.Fatal("another operation claimed partial A")
	}
	for _, name := range []string{"programs", "environments", "journal", "activations"} {
		if _, err := os.Stat(filepath.Join(w.home, ".local/state/valise", name)); !os.IsNotExist(err) {
			t.Fatal("unknown intent caused mutation", name, err)
		}
	}
}

func TestPreparationPrivateIntentRefusesAliasesAndChangedBinding(t *testing.T) {
	for _, change := range []string{"binding", "unknown", "trailing", "symlink", "hardlink", "permissions"} {
		t.Run(change, func(t *testing.T) {
			w, rec := preparationFixture(t)
			if err := savePreparation(w.home, rec, true); err != nil {
				t.Fatal(err)
			}
			dir, _ := preparationDir(w.home, rec.Params.Agent, false)
			path := filepath.Join(dir, rec.OperationID+".json")
			var err error
			switch change {
			case "binding":
				rec.Params.SettingsDigest = "changed"
				b, _ := json.Marshal(rec)
				err = os.WriteFile(path, b, 0600)
			case "unknown", "trailing":
				b, _ := os.ReadFile(path)
				if change == "unknown" {
					b = append([]byte(`{"unknown":true,`), b[1:]...)
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
			if _, err := loadPreparation(w.home, rec.Params.Agent, rec.OperationID); err == nil {
				t.Fatal("unsafe intent accepted", change)
			}
		})
	}
}

func TestPreparationCleanupProtectsRetiredRegistrationData(t *testing.T) {
	w, p := preparationFixture(t)
	path, err := preparationResource(w.home, p, "program-original", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	otherRoot := filepath.Join(path, "retired-instance")
	write(t, filepath.Join(otherRoot, "keep"), "retained data", 0600)
	rec := enroll.Record{InstanceID: "demo-archived", Name: "archived", ProfileID: w.p.ID, ProfileVersion: w.p.ProfileVersion, Roots: map[string]string{"state": otherRoot}, Superseded: &enroll.Supersession{By: "another", OpID: p.Plan.OpID}}
	if _, err := porterenroll.WriteRecord(w.home, rec, w.p); err != nil {
		t.Fatal(err)
	}
	// This registration predates the preparation. New registrations into an
	// occupied programme are rejected by the ordinary enrollment entry point.
	if err := savePreparation(w.home, p, true); err != nil {
		t.Fatal(err)
	}
	if err := w.r.claimPreparation(p, "program-original", "", path, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := w.r.preparationCleanupItems(p); err == nil {
		t.Fatal("retired registration authorized as preparation material")
	}
	if read(t, filepath.Join(otherRoot, "keep")) != "retained data" {
		t.Fatal("retired data modified")
	}
}
