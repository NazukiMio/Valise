package vault

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func pruneFixture(t *testing.T) (*fixture, string) {
	f := newFixture(t)
	root := t.TempDir()
	f.dir = filepath.Join(root, "demo", "savepoints")
	return f, root
}

func packCount(t *testing.T, dir string) int {
	n := 0
	filepath.Walk(filepath.Join(dir, PacksDir), func(p string, i os.FileInfo, err error) error {
		if err == nil && !i.IsDir() {
			n++
		}
		return nil
	})
	return n
}

func TestDeleteSnapshotsSweepsOrphanPacks(t *testing.T) {
	f, root := pruneFixture(t)
	a := f.seal(t, map[string][]byte{"a.md": []byte("alpha")})
	b := f.seal(t, map[string][]byte{"b.md": []byte("beta")})
	if packCount(t, f.dir) < 2 {
		t.Fatal("expected two packs")
	}
	rep, err := DeleteSnapshots(root, "demo", "savepoints", []string{a.SnapshotID})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Deleted != 1 || rep.PacksRemoved == 0 || rep.FreedBytes == 0 {
		t.Fatalf("report %+v", rep)
	}
	if problems, err := VerifyNoKey(f.dir); err != nil || len(problems) != 0 {
		t.Fatalf("verify after delete: %v %v", problems, err)
	}
	if _, err := os.Stat(filepath.Join(f.dir, MetaPath(b.SnapshotID))); err != nil {
		t.Fatal("remaining snapshot lost")
	}
	// idempotent
	rep, err = DeleteSnapshots(root, "demo", "savepoints", []string{a.SnapshotID})
	if err != nil || rep.Deleted != 0 || len(rep.Missing) != 1 {
		t.Fatalf("repeat %+v %v", rep, err)
	}
	if _, err := DeleteSnapshots(root, "demo", "savepoints", []string{"bad"}); err == nil {
		t.Fatal("invalid id accepted")
	}
}

func TestDeleteSnapshotsKeepsIndexForLegacySavepoint(t *testing.T) {
	f, root := pruneFixture(t)
	a := f.seal(t, map[string][]byte{"a.md": []byte("alpha")})
	b := f.seal(t, map[string][]byte{"b.md": []byte("beta")})
	// Make b look like a legacy savepoint: new-blobs-only index, no FullDeps.
	mp := filepath.Join(f.dir, MetaPath(b.SnapshotID))
	bm := *b
	bm.Kind = KindSavepoint
	mb, _ := json.Marshal(bm)
	os.WriteFile(mp, mb, 0o600)
	ip := filepath.Join(f.dir, IndexPath(b.SnapshotID))
	var ix Index
	raw, _ := os.ReadFile(ip)
	json.Unmarshal(raw, &ix)
	ix.FullDeps = false
	raw, _ = json.Marshal(ix)
	os.WriteFile(ip, raw, 0o600)
	rep, err := DeleteSnapshots(root, "demo", "savepoints", []string{a.SnapshotID})
	if err != nil {
		t.Fatal(err)
	}
	if rep.IndexesKept != 1 {
		t.Fatalf("index not kept: %+v", rep)
	}
	if _, err := os.Stat(filepath.Join(f.dir, IndexPath(a.SnapshotID))); err != nil {
		t.Fatal("index removed")
	}
}

func TestBundleOf(t *testing.T) {
	f, root := pruneFixture(t)
	a := f.seal(t, map[string][]byte{"a.md": []byte("alpha")})
	am := *a
	am.BundleID, am.BundleMembers = NewBundleID(a.CreatedAt), []string{"demo", "other"}
	mb, _ := json.Marshal(am)
	os.WriteFile(filepath.Join(f.dir, MetaPath(a.SnapshotID)), mb, 0o600)
	got, err := BundleOf(root, "demo", "savepoints", []string{a.SnapshotID})
	if err != nil || len(got) != 1 || got[0].BundleID == "" {
		t.Fatalf("%v %v", got, err)
	}
}

func TestDeleteLastSnapshotIdempotentAndSweepsOrphans(t *testing.T) {
	f, root := pruneFixture(t)
	a := f.seal(t, map[string][]byte{"a.md": []byte("alpha")})
	if packCount(t, f.dir) == 0 {
		t.Fatal("expected at least one pack")
	}
	// Delete the only (last) snapshot in the leaf.
	rep, err := DeleteSnapshots(root, "demo", "savepoints", []string{a.SnapshotID})
	if err != nil {
		t.Fatalf("delete last snapshot: %v", err)
	}
	if rep.Deleted != 1 || rep.PacksRemoved == 0 {
		t.Fatalf("unexpected report: %+v", rep)
	}
	if packCount(t, f.dir) != 0 {
		t.Fatalf("expected 0 packs after deleting last snapshot, got %d", packCount(t, f.dir))
	}
	// Repeating deletion on now-empty leaf must be idempotent (no "leaf is empty" error).
	rep2, err := DeleteSnapshots(root, "demo", "savepoints", []string{a.SnapshotID})
	if err != nil {
		t.Fatalf("idempotent delete on empty leaf failed: %v", err)
	}
	if rep2.Deleted != 0 || len(rep2.Missing) != 1 || rep2.Missing[0] != a.SnapshotID {
		t.Fatalf("unexpected idempotent report: %+v", rep2)
	}
	// Leftover .age, index, and orphan pack when metas is empty must still be swept.
	if err := os.WriteFile(filepath.Join(f.dir, SnapshotPath(a.SnapshotID)), []byte("leftover-age"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.dir, IndexPath(a.SnapshotID)), []byte(`{"snapshot_id":"`+a.SnapshotID+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	orphanID := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	orphanPath := filepath.Join(f.dir, filepath.FromSlash(PackPath(orphanID)))
	if err := os.MkdirAll(filepath.Dir(orphanPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(orphanPath, []byte("orphan-pack"), 0o600); err != nil {
		t.Fatal(err)
	}
	rep3, err := DeleteSnapshots(root, "demo", "savepoints", []string{a.SnapshotID})
	if err != nil {
		t.Fatalf("cleanup on empty leaf failed: %v", err)
	}
	if len(rep3.Missing) != 1 || rep3.Missing[0] != a.SnapshotID || rep3.PacksRemoved != 1 {
		t.Fatalf("unexpected cleanup report: %+v", rep3)
	}
	for _, p := range []string{
		filepath.Join(f.dir, SnapshotPath(a.SnapshotID)),
		filepath.Join(f.dir, IndexPath(a.SnapshotID)),
		orphanPath,
	} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("expected leftover %s to be removed, err=%v", p, err)
		}
	}
	// PlanDelete must reject a directory without VAULT descriptor.
	noVault := t.TempDir()
	if _, err := PlanDelete(noVault, nil, []string{a.SnapshotID}); err == nil {
		t.Fatal("expected error for directory missing VAULT descriptor")
	}
}
