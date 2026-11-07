package backuprun

import (
	"context"
	"os"
	"strings"
	"testing"

	"valise/core/identity"
	cp "valise/core/protection"
)

func TestSnapshotBindingRequiresExactDurableSnapshot(t *testing.T) {
	s, p := fixture(t)
	r, err := s.Run(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	// Historical bindings remain readable when the live Agent has changed.
	s.ValidateAgent = func(context.Context, identity.AgentRef) error { return failure("identity_conflict") }
	before, _ := os.ReadFile(s.path(p.RunID))
	got, err := s.SnapshotBinding(context.Background(), cp.SnapshotBindingParams{SnapshotRef: *r.Snapshot})
	if err != nil || got.State != "bound" || got.AgentRef == nil || *got.AgentRef != p.AgentRef {
		t.Fatalf("binding %+v %v", got, err)
	}
	after, _ := os.ReadFile(s.path(p.RunID))
	if string(before) != string(after) {
		t.Fatal("query changed record")
	}
	for _, ref := range []identity.SnapshotRef{{VaultID: "other", SnapshotID: r.Snapshot.SnapshotID}, {VaultID: r.Snapshot.VaultID, SnapshotID: "other"}} {
		got, err = s.SnapshotBinding(context.Background(), cp.SnapshotBindingParams{SnapshotRef: ref})
		if err != nil || got.State != "unknown" || got.AgentRef != nil {
			t.Fatalf("inferred %+v %v", got, err)
		}
	}
}
func TestSnapshotBindingRejectsGenerationConflict(t *testing.T) {
	s, p := fixture(t)
	r, err := s.Run(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	other := *r
	other.RunID = strings.Repeat("b", 32)
	other.Request = r.Request
	other.Request.RunID = other.RunID
	other.AgentRef.Generation = "new-generation"
	other.Request.AgentRef = other.AgentRef
	other.Plan.AgentRef = other.AgentRef
	other.RequestDigest, _ = cp.Digest(other.Request)
	if err = s.save(&other); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SnapshotBinding(context.Background(), cp.SnapshotBindingParams{SnapshotRef: *r.Snapshot}); err == nil {
		t.Fatal("conflicting generation silently bound")
	}
}
func TestSnapshotBindingRetainsBackupBeforeCopyCompletes(t *testing.T) {
	s, p := fixture(t)
	r, err := s.Run(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	r.State = "running"
	if err = s.save(r); err != nil {
		t.Fatal(err)
	}
	got, err := s.SnapshotBinding(context.Background(), cp.SnapshotBindingParams{SnapshotRef: *r.Snapshot})
	if err != nil || got.AgentRef == nil || *got.AgentRef != r.AgentRef {
		t.Fatalf("published snapshot lost %+v %v", got, err)
	}
	r.Components[0].State = "unknown"
	if err = s.save(r); err != nil {
		t.Fatal(err)
	}
	got, err = s.SnapshotBinding(context.Background(), cp.SnapshotBindingParams{SnapshotRef: *r.Snapshot})
	if err != nil || got.State != "unknown" || got.AgentRef != nil {
		t.Fatalf("unproved backup bound %+v %v", got, err)
	}
}
func TestSnapshotBindingMissingAndCorruptRecords(t *testing.T) {
	s := Store{Home: t.TempDir()}
	p := cp.SnapshotBindingParams{SnapshotRef: identity.SnapshotRef{VaultID: "vault", SnapshotID: "snapshot"}}
	got, err := s.SnapshotBinding(context.Background(), p)
	if err != nil || got.State != "unknown" || got.AgentRef != nil {
		t.Fatalf("missing %+v %v", got, err)
	}
	if _, err = os.Stat(s.dir()); !os.IsNotExist(err) {
		t.Fatal("read query created directory")
	}
	if err = os.MkdirAll(s.dir(), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(s.path(strings.Repeat("c", 32)), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SnapshotBinding(context.Background(), p); err == nil {
		t.Fatal("corrupt binding history ignored")
	}
}
