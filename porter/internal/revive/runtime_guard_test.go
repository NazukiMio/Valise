package revive

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"valise/core/enroll"
	"valise/core/profile"
	"valise/core/revive"
	porterenroll "valise/porter/internal/enroll"
	"valise/porter/internal/runtimeguard"
)

func occupiedError(t *testing.T, err error) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), "environment_in_use") {
		t.Fatal("legacy entry did not refuse occupied resources", err)
	}
}

func TestLegacyMetadataReadsAndExplicitImportRespectPreparation(t *testing.T) {
	w, preparation := preparationFixture(t)
	if err := savePreparation(w.home, preparation, true); err != nil {
		t.Fatal(err)
	}
	// A higher old default copy and an unconverted named sibling must not
	// modify the accepted profile or become an implicit shared-profile upgrade.
	_, acceptedProfile, err := porterenroll.ReadInstallation(w.home, "demo")
	if err != nil || acceptedProfile == nil {
		t.Fatal(err)
	}
	oldProfile := *acceptedProfile
	oldProfile.ProfileVersion++
	old := enroll.Record{InstanceID: "demo", ProfileID: w.p.ID, ProfileVersion: oldProfile.ProfileVersion, Roots: map[string]string{"state": filepath.Join(w.home, "old-default")}}
	named := old
	named.InstanceID, named.Name, named.Roots = "demo-old", "old", map[string]string{"state": filepath.Join(w.home, "old-named")}
	for _, r := range []enroll.Record{old, named} {
		for name, value := range map[string]any{r.InstanceID + ".json": r, r.InstanceID + ".profile.json": oldProfile} {
			b, _ := json.Marshal(value)
			write(t, filepath.Join(porterenroll.InstancesDir(w.home), name), string(b), 0600)
		}
	}
	profilePath := filepath.Join(porterenroll.DossiersDir(w.home), w.p.ID+".profile.json")
	before, _ := os.ReadFile(profilePath)
	for range 2 {
		if records, err := porterenroll.ListRecords(w.home); err != nil || len(records) != 2 {
			t.Fatal(records, err)
		}
		if p, err := porterenroll.ReadProfile(w.home, "demo"); err != nil || p.ProfileVersion != w.p.ProfileVersion {
			t.Fatal("old default replaced occupied profile", p, err)
		}
	}
	// The requested new record uses the accepted profile. The old conversion
	// would nevertheless upgrade it, so its entire conversion is refused first.
	named.ProfileVersion = acceptedProfile.ProfileVersion
	if err := runtimeguard.CheckRecord(context.Background(), w.home, named, acceptedProfile); err != nil {
		t.Fatal("fixture does not isolate the legacy conversion", err)
	}
	_, err = porterenroll.WriteRecord(w.home, named, *acceptedProfile)
	occupiedError(t, err)
	if after, _ := os.ReadFile(profilePath); !bytes.Equal(before, after) {
		t.Fatal("legacy import rewrote an occupied profile")
	}
	if _, err := os.Stat(filepath.Join(porterenroll.InstancesDir(w.home), "demo-old.json")); err != nil {
		t.Fatal("refused conversion archived old sibling", err)
	}
	if d, _, err := porterenroll.ReadInstallation(w.home, named.InstanceID); err != nil || d != nil {
		t.Fatal("refused conversion committed a legacy sibling", d, err)
	}
}

func TestLegacyImportPreflightsAllRecordsBeforeWrite(t *testing.T) {
	w, preparation := preparationFixture(t)
	if err := savePreparation(w.home, preparation, true); err != nil {
		t.Fatal(err)
	}
	_, p, err := porterenroll.ReadInstallation(w.home, "demo")
	if err != nil || p == nil {
		t.Fatal(err)
	}
	first := enroll.Record{InstanceID: "demo-a", Name: "a", ProfileID: p.ID, ProfileVersion: p.ProfileVersion, Roots: map[string]string{"state": filepath.Join(w.home, "disjoint")}}
	last := first
	last.InstanceID, last.Name, last.Roots = "demo-z", "z", map[string]string{"state": w.state}
	for _, r := range []enroll.Record{first, last} {
		for name, value := range map[string]any{r.InstanceID + ".json": r, r.InstanceID + ".profile.json": p} {
			b, _ := json.Marshal(value)
			write(t, filepath.Join(porterenroll.InstancesDir(w.home), name), string(b), 0600)
		}
	}
	if err := runtimeguard.CheckRecord(context.Background(), w.home, first, p); err != nil {
		t.Fatal("requested record is not disjoint", err)
	}
	path := porterenroll.DossierPath(w.home, p.ID)
	before, _ := os.ReadFile(path)
	_, err = porterenroll.WriteRecord(w.home, first, *p)
	occupiedError(t, err)
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
		t.Fatal("later conflict left a partially imported first record")
	}
	if _, err := os.Stat(filepath.Join(porterenroll.InstancesDir(w.home), "demo-a.json")); err != nil {
		t.Fatal("later conflict archived first record", err)
	}
}

func TestPreparationProtectsPreviouslyCreatedRestoreAndServiceEntries(t *testing.T) {
	w, preparation := preparationFixture(t)
	allowDirectRecord(t, w, enroll.Record{InstanceID: "demo", ProfileID: w.p.ID, ProfileVersion: w.p.ProfileVersion, Roots: map[string]string{"state": w.state}}, w.p)
	p := preparation.Plan
	w.stage(t, p, snapshot) // An old client prepared this before the new owner.
	if err := savePreparation(w.home, preparation, true); err != nil {
		t.Fatal(err)
	}
	j, err := w.r.Store.Load(p.OpID)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(w.r.Store.path(p.OpID))
	if err != nil {
		t.Fatal(err)
	}
	if LegacyAvailable(context.Background(), w.r.Store, j) {
		t.Fatal("review offered an old execution branch")
	}
	_, err = w.r.Swap(context.Background(), p.OpID)
	occupiedError(t, err)
	for _, action := range []string{"continue", "back", "discard"} {
		_, err = w.r.Resume(context.Background(), revive.ResumeParams{OpID: p.OpID, Action: action})
		occupiedError(t, err)
	}
	_, err = StageWrite(w.r.Store, p.OpID, bytes.NewReader(nil))
	occupiedError(t, err)
	_, err = StageVerify(w.r.Store, revive.StageVerifyParams{OpID: p.OpID, Manifest: j.Manifest})
	occupiedError(t, err)
	occupiedError(t, w.r.RecordDisposition(p.OpID, ""))
	_, err = PrevDrop(w.r.Store, p.OpID)
	occupiedError(t, err)
	occupiedError(t, StartUnit(context.Background(), w.r.Env, preparation.Native.Unit))
	occupiedError(t, StopUnit(context.Background(), w.r.Env, preparation.Native.Unit))
	after, err := os.ReadFile(w.r.Store.path(p.OpID))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("refused operation rewrote its old journal", err)
	}
	if read(t, filepath.Join(w.state, "SOUL.md")) != "current soul" {
		t.Fatal("refused operation changed live data")
	}
	if _, err := os.Stat(filepath.Join(w.home, "unit-state")); !os.IsNotExist(err) {
		t.Fatal("refused operation invoked service commands", err)
	}
}

func TestPreparationProtectsEnrollmentUsingAnotherInstanceID(t *testing.T) {
	w, p := preparationFixture(t)
	if err := savePreparation(w.home, p, true); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{w.state, filepath.Join(w.state, "child"), filepath.Dir(w.state)} {
		rec := enroll.Record{InstanceID: "other", ProfileID: w.p.ID, ProfileVersion: w.p.ProfileVersion, Roots: map[string]string{"state": root}}
		_, _, err := porterenroll.AcceptRecord(w.home, rec, w.p)
		occupiedError(t, err)
		_, err = porterenroll.WriteRecord(w.home, rec, w.p)
		occupiedError(t, err)
	}
	occupiedError(t, porterenroll.RemoveRecord(w.home, "demo"))
	_, err := porterenroll.SetEntryPort(w.home, "demo", "gateway", 18392)
	occupiedError(t, err)
	other := profile.Profile{ID: "other", ProfileVersion: 1}
	rec := enroll.Record{InstanceID: "other", ProfileID: other.ID, ProfileVersion: 1, Roots: map[string]string{"state": filepath.Join(w.home, "other")}}
	if _, _, err := porterenroll.AcceptRecord(w.home, rec, other); err != nil {
		t.Fatal("disjoint Agent on same server blocked", err)
	}
	changed := p.Plan
	changed.InstanceID, changed.OpID = "other", w.plan().OpID
	_, err = StageBegin(context.Background(), w.r.Env, w.r.Store, changed)
	occupiedError(t, err)
}

func TestFileTestUsesActualStageAndCanReadOccupiedAgent(t *testing.T) {
	w, preparation := preparationFixture(t)
	if err := savePreparation(w.home, preparation, true); err != nil {
		t.Fatal(err)
	}
	p := preparation.Plan
	p.TestOnly = true
	p.OpID = w.plan().OpID
	w.stage(t, p, snapshot)
	j, err := w.r.Store.Load(p.OpID)
	if err != nil || !LegacyAvailable(context.Background(), w.r.Store, j) {
		t.Fatal("independent file test blocked", err)
	}
	if _, err := w.r.Resume(context.Background(), revive.ResumeParams{OpID: p.OpID, Action: "back"}); err != nil {
		t.Fatal("independent test cleanup blocked", err)
	}
	// A nominal test with a stage physically inside an occupied data root
	// cannot use TestOnly to write there.
	p.OpID = w.plan().OpID
	p.Roots = []revive.RootPlan{{ID: "state", Path: filepath.Join(w.state, "pretend-test-root")}}
	_, err = StageBegin(context.Background(), w.r.Env, w.r.Store, p)
	occupiedError(t, err)
	if read(t, filepath.Join(w.state, "SOUL.md")) != "current soul" {
		t.Fatal("file test altered live data")
	}
}

func TestLegacyCleanupChecksActualLeftovers(t *testing.T) {
	w, preparation := preparationFixture(t)
	if err := savePreparation(w.home, preparation, true); err != nil {
		t.Fatal(err)
	}
	p := w.plan()
	p.InstanceID, p.Roots[0].Path = "other", filepath.Join(w.home, "other")
	protected := filepath.Join(w.state, ".valise-kept")
	write(t, filepath.Join(protected, "keep"), "current", 0600)
	j := &revive.Journal{Plan: p, State: revive.StateRolledBack, Intruders: []string{protected}}
	if err := w.r.Store.Save(j); err != nil {
		t.Fatal(err)
	}
	_, err := PrevDrop(w.r.Store, p.OpID)
	occupiedError(t, err)
	if read(t, filepath.Join(protected, "keep")) != "current" {
		t.Fatal("old cleanup removed occupied material")
	}
}
