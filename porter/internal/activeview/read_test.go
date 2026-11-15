package activeview

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"valise/core/enroll"
	"valise/core/environment"
	"valise/core/identity"
	"valise/core/profile"
	"valise/core/vault"
	"valise/porter/internal/activationrecord"
	"valise/porter/internal/environments"
)

var fixtureAgent = identity.AgentRef{ServerUID: "server", InstallID: "demo", InstanceID: "demo", Generation: "generation"}

func privateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	home, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	return home
}

func TestIndependentLabelsDoNotInvalidateRuntimeButSourcesDo(t *testing.T) {
	a := &enroll.Record{InstanceID: "demo", ProfileID: "demo", ProfileVersion: 1, Roots: map[string]string{"state": "/home/fixture/state"}}
	b := *a
	b.DisplayName, b.HarnessName, b.HarnessAgents = "new label", "native label", []string{"other"}
	if !sameRuntimeRecord(a, &b) {
		t.Fatal("display metadata became a runtime dependency")
	}
	b.Roots = map[string]string{"state": "/home/fixture/other"}
	if sameRuntimeRecord(a, &b) {
		t.Fatal("changed root kept runtime binding")
	}
	b = *a
	b.LocalOverrides = append(b.LocalOverrides, profile.Override{Root: "state", Glob: "new", Category: "identity"})
	if sameRuntimeRecord(a, &b) {
		t.Fatal("changed classification kept runtime binding")
	}
}

func preparedRegistry(t *testing.T) environment.Registry {
	t.Helper()
	r, err := environment.New(fixtureAgent)
	if err != nil {
		t.Fatal(err)
	}
	r, err = r.Begin(strings.Repeat("a", 32), identity.SnapshotRef{VaultID: vault.NewID("v", time.Now()), SnapshotID: vault.NewID("s", time.Now())})
	if err != nil {
		t.Fatal(err)
	}
	r, err = r.Prepared(r.Operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestUnknownManagedBindingNeverFallsBackOrInitializes(t *testing.T) {
	home := privateHome(t)
	reader := Reader{Home: home}
	if b, err := reader.Read(context.Background(), fixtureAgent); err != nil || b != nil {
		t.Fatal(b, err)
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatal("unmanaged read created state", entries, err)
	}
	store := environments.Store{Home: home}
	if _, err := store.Update(fixtureAgent, 0, func(environment.Registry) (environment.Registry, error) { return environment.New(fixtureAgent) }); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".local/state/valise/environments", environment.Key(fixtureAgent)+".json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if b, err := reader.Read(context.Background(), fixtureAgent); err == nil || b != nil {
			t.Fatal("unknown private owner fell back", b, err)
		}
		summary, units, entries := reader.Summary(context.Background(), fixtureAgent)
		if summary == nil || summary.Known || summary.Running || summary.GatewayPassed || summary.ConversationConfirmed || len(units) != 0 || len(entries) != 0 {
			t.Fatal(summary, units, entries)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatal("read replayed or rewrote state", err)
	}
	for _, name := range []string{"programs", "activations", "journal", "managed-runtime"} {
		if _, err := os.Stat(filepath.Join(home, ".local/state/valise", name)); !os.IsNotExist(err) {
			t.Fatal("read created execution material", name, err)
		}
	}
}

func TestTransitionCannotProvideStableDailyBinding(t *testing.T) {
	r := preparedRegistry(t)
	for _, phase := range []environment.Phase{environment.Stopping, environment.OriginalDown, environment.Starting, environment.RollingBack, environment.CandidateDown, environment.Resuming} {
		r.Operation.Phase = phase
		if _, err := stableRef(r); err == nil {
			t.Fatal("transition advertised stable active files", phase)
		}
	}
}

func TestDailyBindingUsesCompletedHistoryOnlyForSameActiveRevision(t *testing.T) {
	home := privateHome(t)
	r := preparedRegistry(t)
	op := *r.Operation
	rec := &activationrecord.Record{Schema: activationrecord.Schema, Plan: activationrecord.Plan{Agent: fixtureAgent, OperationID: op.ID, Snapshot: op.Snapshot, Original: op.Original, Candidate: op.Candidate, DataOpID: vault.NewID("op", time.Now()), OriginalMaterials: strings.Repeat("b", 64), CandidateMaterials: strings.Repeat("c", 64)}, OriginalDefinition: strings.Repeat("d", 64), CandidateDefinition: strings.Repeat("e", 64), DataPlanDigest: strings.Repeat("f", 64), DataManifestDigest: strings.Repeat("0", 64), State: "awaiting_human"}
	rec.BindingDigest = activationrecord.Digest(rec)
	if err := activationrecord.Save(home, rec); err != nil {
		t.Fatal(err)
	}
	// A confirmed candidate stays active while the next opposite slot builds.
	op.Phase, op.Confirmed = environment.Accepted, true
	r.History = []environment.Operation{op}
	current := op
	current.ID, current.Original, current.Candidate, current.Phase = strings.Repeat("1", 32), op.Candidate, environment.Ref{Slot: environment.A, Revision: 2}, environment.Building
	r.Operation = &current
	reader := Reader{Home: home}
	got, _, candidate, err := reader.recordFor(r, op.Candidate)
	if err != nil || got.ID != op.ID || !candidate {
		t.Fatal("next preparation lost stable prior binding", got, candidate, err)
	}
	if _, _, _, err := reader.recordFor(r, environment.Ref{Slot: environment.B, Revision: 999}); err == nil {
		t.Fatal("different active incarnation borrowed history")
	}
	if !confirmedRef(r, op.Candidate) || confirmedRef(r, environment.Ref{Slot: environment.B, Revision: 2}) {
		t.Fatal("conversation proof crossed environment revisions")
	}
	r.Operation.Phase = environment.Cancelled
	if !confirmedRef(r, op.Candidate) {
		t.Fatal("cancelled next preparation erased existing acceptance")
	}
	r.Operation = &op
	r.Active = op.Candidate.Slot
	r.Slots[0].State, r.Slots[1].State = environment.Stopped, environment.Active
	r.Operation.Phase = environment.Cleaning
	if ref, err := stableRef(r); err != nil || ref != op.Candidate {
		t.Fatal("cleaning inactive state disabled daily runtime", ref, err)
	}
	// A matching current record that is damaged must not silently fall back.
	r.Operation = &op
	path := filepath.Join(home, ".local/state/valise/activations", environment.Key(fixtureAgent), op.ID+".json")
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := reader.recordFor(r, op.Candidate); err == nil {
		t.Fatal("damaged current record used history fallback")
	}
}
