package vault

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// restore.py must reproduce every file byte for byte using only Python,
// age and zstd (docs/VAULT_FORMAT.md section 9).
func TestStandaloneRestore(t *testing.T) {
	for _, tool := range []string{"python3", "age", "zstd"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not installed")
		}
	}
	_, here, _, _ := runtime.Caller(0)
	script := filepath.Join(filepath.Dir(here), "..", "..", "tools", "standalone-restore", "restore.py")

	f := newFixture(t)
	data := testData()
	w, err := OpenWriter(f.dir, f.recipients, f.keyFile, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var files Files
	add := func(root, path, cat string, mode uint32, b []byte) {
		blobs, sum, n, err := w.AddStream(bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		files.Files = append(files.Files, File{Root: root, Path: path, Category: cat, Mode: mode,
			MTime: time.Date(2026, 10, 1, 12, 0, 0, 123456789, time.UTC), Size: n, SHA256: sum, Blobs: blobs})
	}
	add("state", "memories/big.bin", "memory", 0o640, data["big.bin"])
	add("state", "SOUL.md", "identity", 0o644, data["small.md"])
	add("state", "empty", "config", 0o600, nil)
	add("state", ".env", "secrets", 0o600, []byte("API_KEY=secret\n"))
	add(BodyPatchRoot, BodyPatchPath, "body_patch", 0o600, []byte("diff --git a b\n"))
	files.Symlinks = []Symlink{{Root: "state", Path: "link.md", Target: "SOUL.md"}}
	m, err := w.Commit(Snapshot{InstanceID: "demo", Tier: TierSoulSecrets, CreatedAt: time.Now().UTC()}, files, Meta{})
	if err != nil {
		t.Fatal(err)
	}
	idFile := filepath.Join(t.TempDir(), "identity.txt")
	os.WriteFile(idFile, []byte(f.id.String()+"\n"), 0o600)

	restore := func(out string, extra ...string) string {
		args := append([]string{"-I", script, "--vault", f.dir, "--identity", idFile, "--snapshot", m.SnapshotID, "--out", out}, extra...)
		b, err := exec.Command("python3", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("restore.py: %v\n%s", err, b)
		}
		return string(b)
	}
	out := filepath.Join(t.TempDir(), "out")
	msg := restore(out)
	if !strings.Contains(msg, "skipped 1 secret") {
		t.Fatal(msg)
	}
	check := func(rel string, want []byte, mode os.FileMode) {
		t.Helper()
		got, err := os.ReadFile(filepath.Join(out, rel))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("%s differs: %v", rel, err)
		}
		st, _ := os.Stat(filepath.Join(out, rel))
		if st.Mode().Perm() != mode || !st.ModTime().Equal(time.Date(2026, 10, 1, 12, 0, 0, 123456789, time.UTC)) {
			t.Fatalf("%s: mode %v mtime %v", rel, st.Mode(), st.ModTime())
		}
	}
	check("state/memories/big.bin", data["big.bin"], 0o640)
	check("state/SOUL.md", data["small.md"], 0o644)
	check("state/empty", nil, 0o600)
	check("valise/body.patch", []byte("diff --git a b\n"), 0o600)
	if _, err := os.Stat(filepath.Join(out, "state/.env")); !os.IsNotExist(err) {
		t.Fatal("secret restored without --include-secrets")
	}
	if target, err := os.Readlink(filepath.Join(out, "state/link.md")); err != nil || target != "SOUL.md" {
		t.Fatalf("symlink: %q %v", target, err)
	}

	out2 := filepath.Join(t.TempDir(), "out2")
	restore(out2, "--include-secrets")
	if b, _ := os.ReadFile(filepath.Join(out2, "state/.env")); string(b) != "API_KEY=secret\n" {
		t.Fatal("secret not restored with --include-secrets")
	}
	list, err := exec.Command("python3", "-I", script, "--vault", f.dir, "--identity", idFile, "--list").CombinedOutput()
	if err != nil || !strings.Contains(string(list), m.SnapshotID) {
		t.Fatalf("%s %v", list, err)
	}
}
