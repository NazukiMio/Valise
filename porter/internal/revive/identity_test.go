package revive

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"valise/core/enroll"
	"valise/core/identity"
	"valise/core/profile"
	"valise/core/revive"
	porterenroll "valise/porter/internal/enroll"
	identitystore "valise/porter/internal/identity"
)

var sourceRef = identity.AgentRef{ServerUID: "source-server", InstallID: "demo", InstanceID: "demo", Generation: "source-generation"}

func (w *world) recoveryPlan(mode string, source *identity.AgentRef) revive.Plan {
	p := revive.Plan{OpID: revive.NewOpID(time.Now()), Mode: mode, InstanceID: "target", SnapshotID: "s_fixture", Tier: "full",
		Profile: w.p, Roots: []revive.RootPlan{{ID: "state", Path: w.state}}, Body: revive.BodyPlan{Action: revive.BodyKeep}, Unit: "demo.service",
		Record: &enroll.Record{InstanceID: "target", ProfileID: "demo", ProfileVersion: 1, Roots: map[string]string{"state": w.state}}}
	p.Identity = &identity.RecoveryContext{Mode: mode, SourceAgent: source, SourceSnapshot: identity.SnapshotRef{VaultID: "v_fixture", SnapshotID: "s_fixture"}}
	return p
}

func (w *world) swapWith(t *testing.T, p revive.Plan, files []snapFile, disposition string) *revive.SwapResult {
	t.Helper()
	w.stage(t, p, files)
	if err := w.r.RecordDisposition(p.OpID, disposition); err != nil {
		t.Fatal(err)
	}
	res, err := w.r.Swap(context.Background(), p.OpID)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func relations(t *testing.T, home string) []identity.Relation {
	t.Helper()
	rels, err := identitystore.ListRelations(home)
	if err != nil {
		t.Fatal(err)
	}
	return rels
}

func TestMigrationCompletedOnlyAfterHealthAndIdempotent(t *testing.T) {
	w := newWorld(t)
	p := w.recoveryPlan(revive.ModeMigrate, &sourceRef)
	res := w.swapWith(t, p, snapshot, identity.StoppedVerified)
	if res.State != revive.StateHealthy {
		t.Fatalf("%+v", res)
	}
	b, err := identitystore.Read(w.home, "target")
	if err != nil || b.Agent == nil || b.Agent.Ref.Generation == sourceRef.Generation {
		t.Fatalf("target generation: %+v %v", b, err)
	}
	rels := relations(t, w.home)
	if len(rels) != 1 || rels[0].Kind != identity.MigrationCompleted || rels[0].SourceAgent == nil || *rels[0].SourceAgent != sourceRef ||
		rels[0].TargetAgent != b.Agent.Ref || rels[0].OperationRef != p.OpID || rels[0].SourceDisposition != identity.StoppedVerified {
		t.Fatalf("relation: %+v", rels)
	}
	// Continuing a finished, identity-bound operation re-runs only the fixed
	// identity completion: same generation, still one relation, no new swap.
	if _, err := w.r.Resume(context.Background(), revive.ResumeParams{OpID: p.OpID, Action: "continue"}); err != nil {
		t.Fatal(err)
	}
	again, _ := identitystore.Read(w.home, "target")
	if again.Agent.Ref != b.Agent.Ref || len(relations(t, w.home)) != 1 {
		t.Fatalf("idempotent finalize changed identity: %+v", again)
	}
}

func TestUnresolvedMigrationAndFailedRecoveryWriteNoSuccessEdge(t *testing.T) {
	w := newWorld(t)
	p := w.recoveryPlan(revive.ModeMigrate, &sourceRef)
	w.swapWith(t, p, snapshot, "")
	b, err := identitystore.Read(w.home, "target")
	if err != nil || b.Agent == nil {
		t.Fatalf("new target generation missing: %+v %v", b, err)
	}
	if rels := relations(t, w.home); len(rels) != 0 {
		t.Fatalf("unresolved source produced an edge: %+v", rels)
	}

	f := newWorld(t)
	write(t, filepath.Join(f.home, "unit-broken"), "", 0o644)
	fp := f.recoveryPlan(revive.ModeMigrate, &sourceRef)
	if res := f.swapWith(t, fp, snapshot, identity.StoppedVerified); res.State != revive.StateRolledBack {
		t.Fatalf("expected rollback: %+v", res)
	}
	if b, err := identitystore.Read(f.home, "target"); err != nil || b.Agent != nil {
		t.Fatalf("failed recovery bound identity: %+v %v", b, err)
	}
	if rels := relations(t, f.home); len(rels) != 0 {
		t.Fatalf("failed recovery wrote edge: %+v", rels)
	}
}

func TestCloneRequiresTargetVerifiedExclusion(t *testing.T) {
	w := newWorld(t)
	w.p.ExclusiveSecrets = []profile.PathRef{{Root: "state", Glob: ".env"}}
	p := w.recoveryPlan(revive.ModeClone, nil)
	w.stage(t, p, append(snapshot, snapFile{".env", "KEY=exclusive"}))
	if err := w.r.RecordDisposition(p.OpID, identity.ExclusiveOmitted); err == nil {
		t.Fatal("stage containing declared exclusive secret was accepted")
	}
	if err := w.r.RecordDisposition(p.OpID, identity.StoppedVerified); err == nil {
		t.Fatal("clone accepted a migration disposition")
	}

	c := newWorld(t)
	c.p.ExclusiveSecrets = []profile.PathRef{{Root: "state", Glob: ".env"}}
	cp := c.recoveryPlan(revive.ModeClone, nil)
	c.swapWith(t, cp, snapshot, identity.ExclusiveOmitted)
	rels := relations(t, c.home)
	if len(rels) != 1 || rels[0].Kind != identity.CloneFrom || rels[0].SourceAgent != nil || rels[0].SourceSnapshot.SnapshotID != "s_fixture" {
		t.Fatalf("clone relation: %+v", rels)
	}
}

// A migration whose source could not be reached leaves exclusive secrets
// out (docs/ARCHITECTURE.md 8.2.3): the stage is checked like a clone's and
// the migration relation records an unknown source agent.
func TestLostSourceMigrationOmitsExclusive(t *testing.T) {
	w := newWorld(t)
	w.p.ExclusiveSecrets = []profile.PathRef{{Root: "state", Glob: ".env"}}
	p := w.recoveryPlan(revive.ModeMigrate, nil)
	w.stage(t, p, append(snapshot, snapFile{".env", "KEY=exclusive"}))
	if err := w.r.RecordDisposition(p.OpID, identity.ExclusiveOmitted); err == nil {
		t.Fatal("lost-source migration accepted a stage with an exclusive secret")
	}

	m := newWorld(t)
	m.p.ExclusiveSecrets = []profile.PathRef{{Root: "state", Glob: ".env"}}
	mp := m.recoveryPlan(revive.ModeMigrate, nil)
	m.swapWith(t, mp, snapshot, identity.ExclusiveOmitted)
	rels := relations(t, m.home)
	if len(rels) != 1 || rels[0].Kind != identity.MigrationCompleted || rels[0].SourceAgent != nil || rels[0].SourceDisposition != identity.ExclusiveOmitted {
		t.Fatalf("migration relation: %+v", rels)
	}
}

func TestRollbackKeepsGenerationAndLegacyStaysUnbound(t *testing.T) {
	w := newWorld(t)
	rec := enroll.Record{InstanceID: "demo", ProfileID: "demo", ProfileVersion: 1, Roots: map[string]string{"state": w.state}}
	_, accepted, err := porterenroll.AcceptRecord(w.home, rec, w.p)
	if err != nil {
		t.Fatal(err)
	}
	p := w.plan()
	p.SnapshotID = "s_fixture"
	p.Identity = &identity.RecoveryContext{Mode: revive.ModeRollback, SourceSnapshot: identity.SnapshotRef{VaultID: "v_fixture", SnapshotID: "s_fixture"}}
	w.swapWith(t, p, snapshot, "")
	b, _ := identitystore.Read(w.home, "demo")
	if b.Agent == nil || b.Agent.Ref != accepted.Ref {
		t.Fatalf("rollback changed generation: %+v", b)
	}
	if rels := relations(t, w.home); len(rels) != 1 || rels[0].Kind != identity.RestoredFrom || rels[0].TargetAgent != accepted.Ref {
		t.Fatalf("restored_from: %+v", rels)
	}

	l := newWorld(t)
	lp := l.plan()
	lp.SnapshotID = "s_fixture"
	lp.Identity = &identity.RecoveryContext{Mode: revive.ModeRollback, SourceSnapshot: identity.SnapshotRef{VaultID: "v_fixture", SnapshotID: "s_fixture"}}
	l.swapWith(t, lp, snapshot, "")
	if b, err := identitystore.Read(l.home, "demo"); err != nil || b.IdentityState != identity.LegacyUnbound {
		t.Fatalf("legacy rollback invented identity: %+v %v", b, err)
	}
}

func TestLegacyPlanRejectsDispositionAndKeepsOldBehaviour(t *testing.T) {
	w := newWorld(t)
	p := w.recoveryPlan(revive.ModeMigrate, nil)
	p.Identity = nil
	w.stage(t, p, snapshot)
	if err := w.r.RecordDisposition(p.OpID, identity.StoppedVerified); err == nil {
		t.Fatal("legacy plan accepted identity semantics")
	}
	if err := w.r.RecordDisposition(p.OpID, ""); err != nil {
		t.Fatal(err)
	}
	if res, err := w.r.Swap(context.Background(), p.OpID); err != nil || res.State != revive.StateHealthy {
		t.Fatalf("%+v %v", res, err)
	}
	if r, _ := porterenroll.ReadRecord(w.home, "target"); r == nil {
		t.Fatal("legacy fact record missing")
	}
	if b, _ := identitystore.Read(w.home, "target"); b.Agent != nil {
		t.Fatal("legacy plan created identity")
	}
}
