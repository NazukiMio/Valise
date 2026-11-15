package revive

import (
	"archive/tar"
	"bytes"
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
	"valise/core/revive"
	"valise/core/vault"
	porterenroll "valise/porter/internal/enroll"
	"valise/porter/internal/environments"
)

type managedWorld struct {
	*world
	p        revive.Plan
	scope    revive.ManagedDataRef
	manifest []revive.ManifestEntry
}

func newManagedWorld(t *testing.T, extraRoot bool, change ...func(*profile.Profile)) *managedWorld {
	t.Helper()
	w := newWorld(t)
	if err := os.Chmod(w.home, 0700); err != nil {
		t.Fatal(err)
	}
	for _, fn := range change {
		fn(&w.p)
	}
	roots := map[string]string{"state": w.state}
	p := w.plan()
	p.Carry, p.Unit, p.Body = false, "", revive.BodyPlan{Action: revive.BodyKeep}
	p.Tier, p.SnapshotID = vault.TierFull, vault.NewID("s", time.Now())
	if extraRoot {
		roots["extra"] = filepath.Join(w.home, "separate")
		write(t, filepath.Join(roots["extra"], "old"), "original extra", 0600)
		p.Roots = append(p.Roots, revive.RootPlan{ID: "extra", Path: roots["extra"]})
	}
	for _, root := range roots {
		if err := os.Chmod(root, 0700); err != nil {
			t.Fatal(err)
		}
	}
	_, agent, err := porterenroll.AcceptRecord(w.home, enroll.Record{InstanceID: "demo", ProfileID: "demo", ProfileVersion: w.p.ProfileVersion,
		Roots: roots, EnrolledAt: time.Now().UTC()}, w.p)
	if err != nil {
		t.Fatal(err)
	}
	s := environments.Store{Home: w.home}
	r, err := s.Update(agent.Ref, 0, func(environment.Registry) (environment.Registry, error) { return environment.New(agent.Ref) })
	if err != nil {
		t.Fatal(err)
	}
	r, err = s.Update(agent.Ref, r.Revision, func(r environment.Registry) (environment.Registry, error) {
		return r.Begin(strings.Repeat("b", 32), identity.SnapshotRef{VaultID: vault.NewID("v", time.Now()), SnapshotID: p.SnapshotID})
	})
	if err != nil {
		t.Fatal(err)
	}
	return &managedWorld{world: w, p: p,
		scope:    revive.ManagedDataRef{Agent: agent.Ref, OperationID: r.Operation.ID, Snapshot: r.Operation.Snapshot, Original: r.Operation.Original, Candidate: r.Operation.Candidate},
		manifest: []revive.ManifestEntry{{Path: "state/SOUL.md", Size: 8, SHA256: vault.SHA256Hex([]byte("restored")), Category: "identity"}}}
}

func (w *managedWorld) transition(t *testing.T, fn func(environment.Registry, string) (environment.Registry, error)) {
	t.Helper()
	s := environments.Store{Home: w.home}
	r, err := s.Load(w.scope.Agent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(w.scope.Agent, r.Revision, func(r environment.Registry) (environment.Registry, error) { return fn(r, w.scope.OperationID) }); err != nil {
		t.Fatal(err)
	}
}

func (w *managedWorld) begin(t *testing.T) {
	t.Helper()
	if _, err := BeginManagedData(context.Background(), w.r.Env, w.r.Store, w.p, w.scope, w.manifest); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	if err := tw.WriteHeader(&tar.Header{Name: "state/SOUL.md", Mode: 0600, Typeflag: tar.TypeReg, Size: 8}); err != nil {
		t.Fatal(err)
	}
	tw.Write([]byte("restored"))
	tw.Close()
	if _, err := StageWrite(w.r.Store, w.p.OpID, &b); err != nil {
		t.Fatal(err)
	}
	verified, err := StageVerify(w.r.Store, revive.StageVerifyParams{OpID: w.p.OpID, Manifest: w.manifest})
	if err != nil || len(verified.Problems) != 0 {
		t.Fatal(verified, err)
	}
}

func (w *managedWorld) originalDown(t *testing.T) {
	w.transition(t, environment.Registry.Prepared)
	w.transition(t, environment.Registry.StopOriginal)
	w.transition(t, environment.Registry.OriginalStopped)
}

func (w *managedWorld) candidateDown(t *testing.T) {
	w.transition(t, func(r environment.Registry, id string) (environment.Registry, error) {
		return r.BeginRollback(id, "gateway_failed")
	})
	w.transition(t, environment.Registry.CandidateStopped)
}

func TestManagedDataExchangeAndVerifiedRevertPreserveBothEnvironments(t *testing.T) {
	w := newManagedWorld(t, false)
	w.begin(t)
	ctx := context.Background()
	if _, err := w.r.ExchangeManagedData(ctx, w.p.OpID); err == nil {
		t.Fatal("exchanged while original still running")
	}
	if got := read(t, filepath.Join(w.state, "SOUL.md")); got != "current soul" {
		t.Fatal("preparation touched active data")
	}
	w.originalDown(t)
	result, err := w.r.ExchangeManagedData(ctx, w.p.OpID)
	if err != nil || result.State != revive.StateRestored || len(result.Prev) != 1 {
		t.Fatal(result, err)
	}
	if read(t, filepath.Join(result.Prev[0], "SOUL.md")) != "current soul" || read(t, filepath.Join(w.state, "SOUL.md")) != "restored" {
		t.Fatal("did not preserve original independently")
	}
	write(t, filepath.Join(w.state, "after-activation"), "candidate writes", 0600)
	if _, err := w.r.ExchangeManagedData(ctx, w.p.OpID); err != nil || read(t, filepath.Join(w.state, "after-activation")) != "candidate writes" {
		t.Fatal("repeated completed request replayed exchange", err)
	}
	w.transition(t, environment.Registry.StartCandidate)
	if _, err := w.r.RevertManagedData(ctx, w.p.OpID); err == nil {
		t.Fatal("reverted without verified candidate stop")
	}
	w.candidateDown(t)
	result, err = w.r.RevertManagedData(ctx, w.p.OpID)
	if err != nil || result.State != revive.StateRolledBack || result.Rollback.Directories != "restored" || result.Rollback.Service != "not_started" {
		t.Fatal(result, err)
	}
	j, _ := w.r.Store.Load(w.p.OpID)
	if read(t, filepath.Join(w.state, "SOUL.md")) != "current soul" || read(t, filepath.Join(j.Roots[0].Failed, "after-activation")) != "candidate writes" {
		t.Fatal("rollback lost original or failed candidate writes")
	}
	write(t, filepath.Join(w.state, "after-revert"), "original writes", 0600)
	if _, err := w.r.RevertManagedData(ctx, w.p.OpID); err != nil || read(t, filepath.Join(w.state, "after-revert")) != "original writes" {
		t.Fatal("repeat rollback moved data again", err)
	}
	r, _ := (environments.Store{Home: w.home}).Load(w.scope.Agent)
	if r.Operation.Phase != environment.CandidateDown || r.Active != "" {
		t.Fatal("data rollback falsely reported original service healthy", r)
	}
	if _, err := os.Lstat(filepath.Join(w.home, "unit-state")); !os.IsNotExist(err) {
		t.Fatal("data helper invoked service commands")
	}
}

func TestManagedDataRejectsGenericExecutionCleanupAndManifestReplacement(t *testing.T) {
	w := newManagedWorld(t, false)
	w.begin(t)
	ctx := context.Background()
	if _, err := w.r.Swap(ctx, w.p.OpID); err == nil {
		t.Fatal("generic swap accepted managed journal")
	}
	for _, action := range []string{"continue", "back", "discard"} {
		if _, err := w.r.Resume(ctx, revive.ResumeParams{OpID: w.p.OpID, Action: action}); err == nil {
			t.Fatal("generic resume accepted managed journal", action)
		}
	}
	changed := append([]revive.ManifestEntry(nil), w.manifest...)
	changed[0].SHA256 = strings.Repeat("a", 64)
	if _, err := StageVerify(w.r.Store, revive.StageVerifyParams{OpID: w.p.OpID, Manifest: changed}); err == nil {
		t.Fatal("replaced frozen complete manifest")
	}
	w.originalDown(t)
	if _, err := w.r.ExchangeManagedData(ctx, w.p.OpID); err != nil {
		t.Fatal(err)
	}
	if _, err := PrevDrop(w.r.Store, w.p.OpID); err == nil {
		t.Fatal("generic cleanup removed retained original")
	}
	list, err := PrevList(w.r.Store)
	if err != nil || len(list.Entries) != 0 {
		t.Fatal("managed directories offered as ordinary leftovers", list, err)
	}
}

func TestManagedDataRechecksActualStageBeforeExchange(t *testing.T) {
	w := newManagedWorld(t, false)
	w.begin(t)
	j, _ := w.r.Store.Load(w.p.OpID)
	write(t, filepath.Join(j.Roots[0].Stage, "SOUL.md"), "modified", 0600)
	w.originalDown(t)
	if _, err := w.r.ExchangeManagedData(context.Background(), w.p.OpID); err == nil {
		t.Fatal("used stale stage verification")
	}
	if read(t, filepath.Join(w.state, "SOUL.md")) != "current soul" || exists(j.Roots[0].Prev) {
		t.Fatal("verification failure moved original")
	}
}
