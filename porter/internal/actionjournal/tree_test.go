package actionjournal

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopyIndependentTreeAndWriteBoundary(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "live")
	stage := filepath.Join(dir, "stage")
	if err := os.MkdirAll(filepath.Join(live, "workspace"), 0700); err != nil {
		t.Fatal(err)
	}
	for p, body := range map[string]string{"config.json": "old", "workspace/memory.txt": "memory"} {
		if err := os.WriteFile(filepath.Join(live, p), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("workspace/memory.txt", filepath.Join(live, "memory-link")); err != nil {
		t.Fatal(err)
	}
	before, n, err := TreeFacts(live)
	if err != nil || n != 9 {
		t.Fatal(n, err)
	}
	id, err := CopyTree(live, stage)
	if err != nil {
		t.Fatal(err)
	}
	original, err := DirectoryIdentity(live)
	if err != nil || id == original {
		t.Fatal("copy shares directory identity", err)
	}
	if err = CheckWrites(stage, []string{"config.json"}); err != nil {
		t.Fatal(err)
	}
	if err = CheckWrites(stage, []string{"memory-link"}); err == nil {
		t.Fatal("symlink write accepted")
	}
	if err = os.WriteFile(filepath.Join(stage, "config.json"), []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	liveBody, err := os.ReadFile(filepath.Join(live, "config.json"))
	if err != nil || string(liveBody) != "old" {
		t.Fatal("copy modified original", err)
	}
	after, _, err := TreeFacts(stage)
	if err != nil {
		t.Fatal(err)
	}
	if err = CheckChanges(before, after, []string{"config.json"}); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(stage, "workspace/memory.txt"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	after, _, err = TreeFacts(stage)
	if err != nil {
		t.Fatal(err)
	}
	if err = CheckChanges(before, after, []string{"config.json"}); err == nil {
		t.Fatal("undeclared write accepted")
	}
}
func TestRejectEscapingLinksAndSpecialFiles(t *testing.T) {
	for _, target := range []string{"../outside", "/tmp/outside"} {
		t.Run(target, func(t *testing.T) {
			dir := t.TempDir()
			live := filepath.Join(dir, "live")
			if err := os.Mkdir(live, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "outside"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, filepath.Join(live, "link")); err != nil {
				t.Fatal(err)
			}
			if _, _, err := TreeFacts(live); err == nil {
				t.Fatal("escaping link accepted")
			}
		})
	}
}
