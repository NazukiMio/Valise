package identity

import (
	"errors"
	"testing"

	model "valise/core/identity"
)

func recoveryInput(op, mode, disposition string) RecoveryInput {
	return RecoveryInput{OperationRef: op, SourceDisposition: disposition, InstallID: "demo", InstanceID: "target",
		Roots: map[string]string{"state": "/home/test/state"}, ProfileDigest: "digest",
		Context: model.RecoveryContext{Mode: mode, SourceSnapshot: model.SnapshotRef{VaultID: "v", SnapshotID: "s"}}}
}

func TestRecoveryCommitInterruptedIsHiddenAndRetriedExactly(t *testing.T) {
	home := t.TempDir()
	in := recoveryInput("op_a", "migrate", model.StoppedVerified)
	boom := errors.New("facts write failed")
	if _, _, err := CompleteRecovery(home, in, func() error { return boom }); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if b, err := Read(home, "target"); err == nil || b.Agent != nil || b.IdentityState != model.NeedsReconciliation {
		t.Fatalf("pending recovery published: %+v %v", b, err)
	}
	pending, _ := readRecovery(home)
	if _, _, err := CompleteRecovery(home, recoveryInput("op_b", "migrate", model.StoppedVerified), func() error { return nil }); err == nil {
		t.Fatal("another operation overtook a pending recovery")
	}
	if _, err := Accept(home, input("other"), func() error { return nil }); err == nil {
		t.Fatal("enrollment overtook a pending recovery")
	}
	a, rel, err := CompleteRecovery(home, in, func() error { return nil })
	if err != nil || a.Ref.Generation != pending.Agent.Ref.Generation || rel == nil || rel.Kind != model.MigrationCompleted {
		t.Fatalf("retry: %+v %+v %v", a, rel, err)
	}
	a2, rel2, err := CompleteRecovery(home, in, func() error { t.Fatal("completed operation persisted again"); return nil })
	if err != nil || a2.Ref != a.Ref || rel2.RelationID != rel.RelationID {
		t.Fatalf("repeat: %+v %v", a2, err)
	}
	changed := in
	changed.SourceDisposition = model.Unresolved
	if _, _, err := CompleteRecovery(home, changed, func() error { return nil }); err == nil {
		t.Fatal("completed operation accepted different facts")
	}
}

func TestRecoveryValidationAndUnknownSource(t *testing.T) {
	home := t.TempDir()
	bad := recoveryInput("op_a", "migrate", "user_said_stopped")
	if _, _, err := CompleteRecovery(home, bad, func() error { return nil }); err == nil {
		t.Fatal("unknown disposition accepted")
	}
	partial := recoveryInput("op_a", "migrate", model.StoppedVerified)
	partial.Context.SourceAgent = &model.AgentRef{InstanceID: "demo"}
	if _, _, err := CompleteRecovery(home, partial, func() error { return nil }); err == nil {
		t.Fatal("incomplete source AgentRef accepted")
	}
	clone := recoveryInput("op_c", "clone", model.Unresolved)
	if _, rel, err := CompleteRecovery(home, clone, func() error { return nil }); err != nil || rel != nil {
		t.Fatalf("clone without verified exclusion produced relation: %+v %v", rel, err)
	}
	rels, err := ListRelations(home)
	if err != nil || len(rels) != 0 {
		t.Fatalf("%+v %v", rels, err)
	}
}
