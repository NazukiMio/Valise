package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// A restore test unpacks on both sides, switches nothing and leaves no
// copies behind (docs/UI.md 9.5).
func TestRestoreTest(t *testing.T) {
	s, home, _, ss := sealedWorld(t)
	ctx := context.Background()
	s.OwnerAuth.Passphrase = func(string) (string, error) { return pass, nil }
	before := readFile(t, filepath.Join(home, ".demo/memory/a.md"))
	os.WriteFile(filepath.Join(home, ".demo/memory/a.md"), []byte("changed after seal"), 0o644)

	res, err := s.RunRestoreTest(ctx, "box", "demo", "")
	if err != nil || !res.OK || res.Snapshot != ss || res.Files == 0 {
		t.Fatalf("%+v %v", res, err)
	}
	if got := readFile(t, filepath.Join(home, ".demo/memory/a.md")); got != "changed after seal" || got == before {
		t.Fatal("a restore test changed the live state")
	}
	if left, _ := filepath.Glob(filepath.Join(home, ".valise-*")); len(left) > 0 {
		t.Fatalf("stage left on the server: %v", left)
	}
	if left, _ := filepath.Glob(filepath.Join(home, ".*", ".valise-*")); len(left) > 0 {
		t.Fatalf("stage left on the server: %v", left)
	}
	if left, _ := os.ReadDir(filepath.Join(s.Dirs.Cache, "tmp")); len(left) > 0 {
		t.Fatalf("local copy left: %v", left)
	}
	if last, ok := s.LastRestoreTest("box", "demo"); !ok || !last.OK || last.Snapshot != ss {
		t.Fatalf("%+v", last)
	}
	prev, err := s.PrevList(ctx, "box")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range prev.Entries {
		t.Fatalf("restore test left something to clean up: %+v", e)
	}
}
