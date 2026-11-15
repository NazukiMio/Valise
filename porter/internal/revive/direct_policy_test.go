package revive

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"valise/core/enroll"
	"valise/core/environment"
	"valise/core/profile"
	"valise/core/revive"
	"valise/core/valise"
	porterenroll "valise/porter/internal/enroll"
	"valise/porter/internal/environments"
	porteridentity "valise/porter/internal/identity"
)

// Legacy direct-exchange fixtures explicitly enroll and select direct mode;
// they must not obtain an implicit exception from the new default.
func newDirectWorld(t *testing.T) *world {
	t.Helper()
	w := newWorld(t)
	allowDirectRecord(t, w, enroll.Record{InstanceID: "demo", ProfileID: w.p.ID, ProfileVersion: w.p.ProfileVersion, Roots: map[string]string{"state": w.state}}, w.p)
	return w
}

func allowDirectRecord(t *testing.T, w *world, rec enroll.Record, p profile.Profile) {
	t.Helper()
	_, agent, err := porterenroll.AcceptRecord(w.home, rec, p)
	if err != nil {
		t.Fatal(err)
	}
	s := environments.Store{Home: w.home}
	v, err := s.Settings(agent.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetSettings(environment.SetSettingsParams{Agent: agent.Ref, Expected: v.Digest, Mode: environment.Direct}); err != nil {
		t.Fatal(err)
	}
}

func TestDirectPolicyDefaultsToParallel(t *testing.T) {
	w := newWorld(t)
	_, _, err := porterenroll.AcceptRecord(w.home, enroll.Record{InstanceID: "demo", ProfileID: w.p.ID, ProfileVersion: 1, Roots: map[string]string{"state": w.state}, Service: []enroll.ServiceState{{Scope: "user", Unit: "demo.service"}}}, w.p)
	if err != nil {
		t.Fatal(err)
	}
	p := w.plan()
	_, err = StageBegin(context.Background(), w.r.Env, w.r.Store, p)
	if e, ok := valise.As(err); !ok || e.Code != "environment_parallel_required" {
		t.Fatal("new Agent implicitly entered direct mode", err)
	}
	if _, err := os.Stat(w.r.Store.path(p.OpID)); !os.IsNotExist(err) {
		t.Fatal("refused admission created a journal", err)
	}
	if read(t, filepath.Join(w.state, "SOUL.md")) != "current soul" {
		t.Fatal("refused admission changed active data")
	}
	p.Mode, p.InstanceID, p.Roots[0].Path = revive.ModeClone, "other", filepath.Join(w.home, "another-root")
	if err := checkDirectPolicy(context.Background(), w.r.Store, p); err == nil {
		t.Fatal("disjoint root hid the existing Agent's service")
	}
}

func TestDirectPolicyCannotBeBypassedByModeIDOrDirectoryAlias(t *testing.T) {
	w := newWorld(t)
	_, _, err := porterenroll.AcceptRecord(w.home, enroll.Record{InstanceID: "demo", ProfileID: w.p.ID, ProfileVersion: 1, Roots: map[string]string{"state": w.state}}, w.p)
	if err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(w.home, "alias")
	if err := os.Symlink(w.state, alias); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{revive.ModeRollback, revive.ModeMigrate, revive.ModeClone} {
		for _, root := range []string{w.state, alias, filepath.Join(alias, "new-child")} {
			p := w.plan()
			p.InstanceID, p.Mode, p.Roots[0].Path = "another", mode, root
			if err := checkDirectPolicy(context.Background(), w.r.Store, p); err == nil {
				t.Fatal("request hid the parallel Agent", mode, root)
			}
		}
	}
	p := w.plan()
	p.Mode, p.InstanceID, p.Roots[0].Path = revive.ModeClone, "new", filepath.Join(w.home, "new-agent")
	if err := checkDirectPolicy(context.Background(), w.r.Store, p); err != nil {
		t.Fatal("disjoint destination used another Agent's mode", err)
	}
}

func TestDirectPolicyRechecksOldPlanAndPreservesBack(t *testing.T) {
	w := newDirectWorld(t)
	p := w.plan()
	w.stage(t, p, snapshot)
	j, err := w.r.Store.Load(p.OpID)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(w.r.Store.path(p.OpID))
	setWorldMode(t, w, environment.Parallel)
	if LegacyCanContinue(context.Background(), w.r.Store, j) || !LegacyAvailable(context.Background(), w.r.Store, j) {
		t.Fatal("policy removed back or offered continue")
	}
	_, err = w.r.Swap(context.Background(), p.OpID)
	if e, ok := valise.As(err); !ok || e.Code != "environment_parallel_required" {
		t.Fatal("old plan swapped under parallel policy", err)
	}
	if _, err := w.r.Resume(context.Background(), revive.ResumeParams{OpID: p.OpID, Action: "continue"}); err == nil {
		t.Fatal("old resume bypassed parallel policy")
	}
	after, _ := os.ReadFile(w.r.Store.path(p.OpID))
	if !bytes.Equal(before, after) {
		t.Fatal("rejected exchange rewrote journal")
	}
	if _, err := w.r.Resume(context.Background(), revive.ResumeParams{OpID: p.OpID, Action: "back"}); err != nil {
		t.Fatal("parallel setting stranded old preparation", err)
	}
	if read(t, filepath.Join(w.state, "SOUL.md")) != "current soul" {
		t.Fatal("old preparation back changed current data")
	}
}

func setWorldMode(t *testing.T, w *world, mode environment.Mode) {
	t.Helper()
	// Read the saved identity without changing enrollment or its generation.
	records, err := porterenroll.ListAcceptedRecords(w.home)
	if err != nil || len(records) != 1 {
		t.Fatal(records, err)
	}
	binding, err := porteridentity.Read(w.home, records[0].InstanceID)
	if err != nil || binding.Agent == nil {
		t.Fatal(err)
	}
	s := environments.Store{Home: w.home}
	v, err := s.Settings(binding.Agent.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetSettings(environment.SetSettingsParams{Agent: binding.Agent.Ref, Expected: v.Digest, Mode: mode}); err != nil {
		t.Fatal(err)
	}
}

func TestDirectPolicyDoesNotStrandStoppedExchangeOrFinishedCleanup(t *testing.T) {
	for _, action := range []string{"continue", "back"} {
		t.Run(action, func(t *testing.T) {
			w, j := rollbackWorld(t)
			setWorldMode(t, w, environment.Compact)
			if !LegacyCanContinue(context.Background(), w.r.Store, j) {
				t.Fatal("changed policy stranded an exchanged directory")
			}
			if _, err := w.r.Resume(context.Background(), revive.ResumeParams{OpID: j.Plan.OpID, Action: action}); err != nil {
				t.Fatal("interrupted exchange was not recoverable", err)
			}
			if _, err := PrevDrop(w.r.Store, j.Plan.OpID); err != nil {
				t.Fatal("changed policy blocked independent cleanup", err)
			}
		})
	}
}

func TestDirectPolicyFileTestsAndLegacyMetadataRemainReadOnly(t *testing.T) {
	w := newWorld(t)
	p := w.plan()
	if _, err := StageBegin(context.Background(), w.r.Env, w.r.Store, p); err == nil {
		t.Fatal("unbound current Agent admitted")
	}
	dir := porterenroll.InstancesDir(w.home)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(enroll.Record{InstanceID: "old", ProfileID: w.p.ID, Roots: map[string]string{"state": w.state}})
	path := filepath.Join(dir, "old.json")
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	p.Mode, p.InstanceID = revive.ModeClone, "other"
	if err := checkDirectPolicy(context.Background(), w.r.Store, p); err == nil {
		t.Fatal("legacy target hidden from scope")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(b, after) {
		t.Fatal("policy imported old record", err)
	}
	if _, err := os.Stat(porterenroll.DossiersDir(w.home)); !os.IsNotExist(err) {
		t.Fatal("policy created dossier", err)
	}
	p = w.plan()
	p.TestOnly, p.Carry, p.Unit = true, false, ""
	p.Body = revive.BodyPlan{Action: revive.BodyKeep}
	w.stage(t, p, snapshot)
	if _, err := w.r.Resume(context.Background(), revive.ResumeParams{OpID: p.OpID, Action: "back"}); err != nil {
		t.Fatal("file test required direct mode", err)
	}
}
