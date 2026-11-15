package revive

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"valise/core/environment"
	"valise/porter/internal/activationrecord"
	"valise/porter/internal/managedruntime"
)

func TestManagedSourceAnchorOnlyBindsExistingExactParentAndNeverCreatesOnRead(t *testing.T) {
	w, parent := activationRecordFixture(t)
	newOp := strings.Repeat("e", 32)
	source := activationrecord.Source{Schema: "valise-preparation-source/1", Agent: parent.Plan.Agent, OperationID: newOp, Original: parent.Plan.Candidate,
		Owner:        environment.Operation{ID: parent.Plan.OperationID, Snapshot: parent.Plan.Snapshot, Original: parent.Plan.Original, Candidate: parent.Plan.Candidate, Mode: environment.Compact, Phase: environment.Accepted, Confirmed: true},
		ParentDigest: activationrecord.Digest(parent), Materials: parent.Plan.CandidateMaterials, Definition: parent.CandidateDefinition, Observed: managedruntime.Observation{Running: true}}
	before, _ := os.ReadDir(w.home)
	if _, err := activationrecord.LoadSource(w.home, source.Agent, newOp); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	after, _ := os.ReadDir(w.home)
	if len(before) != len(after) {
		t.Fatal("missing source read initialized metadata")
	}
	for _, field := range []string{"parent", "snapshot", "programme", "definition"} {
		bad := source
		switch field {
		case "parent":
			bad.ParentDigest = strings.Repeat("f", 64)
		case "snapshot":
			bad.Owner.Snapshot.SnapshotID = "another"
		case "programme":
			bad.Materials = strings.Repeat("f", 64)
		case "definition":
			bad.Definition = strings.Repeat("f", 64)
		}
		if err := activationrecord.SaveSource(w.home, bad); err == nil {
			t.Fatal("unbound source metadata accepted", field)
		}
	}
	if err := activationrecord.SaveSource(w.home, source); err != nil {
		t.Fatal(err)
	}
	loaded, err := activationrecord.LoadSource(w.home, source.Agent, newOp)
	if err != nil || loaded.ParentDigest != source.ParentDigest {
		t.Fatal(err)
	}
	owner := source.Owner
	owner.OriginalCleaned = true
	if !source.Owns(owner) {
		t.Fatal("unrelated old cleanup changed source identity")
	}
	owner.Candidate.Revision++
	if source.Owns(owner) {
		t.Fatal("reused candidate kept old source identity")
	}
	dir, _ := activationrecord.Dir(w.home, source.Agent, false)
	path := filepath.Join(dir, newOp+".source.json")
	raw, _ := os.ReadFile(path)
	if err := activationrecord.SaveSource(w.home, source); err != nil {
		t.Fatal(err)
	}
	unchanged, _ := os.ReadFile(path)
	if string(raw) != string(unchanged) {
		t.Fatal("repeat source save replaced metadata")
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := activationrecord.LoadSource(w.home, source.Agent, newOp); err == nil {
		t.Fatal("exposed source metadata accepted")
	}
}
