package vault

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
)

// Only a test fixture is rewritten, never a real append-only vault. This
// produces a legacy header over the exact same existing file-list/pack bytes.
func fixtureHeaderVersion(t *testing.T, dir, sid string, id *age.X25519Identity, edit func(*Snapshot)) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, SnapshotPath(sid)))
	if err != nil {
		t.Fatal(err)
	}
	r, err := age.Decrypt(bytes.NewReader(b), id)
	if err != nil {
		t.Fatal(err)
	}
	var s Snapshot
	if err := json.NewDecoder(r).Decode(&s); err != nil {
		t.Fatal(err)
	}
	edit(&s)
	var out bytes.Buffer
	w, err := age.Encrypt(&out, id.Recipient())
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(w).Encode(s); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, SnapshotPath(sid)), out.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotVersionsAndRuntimeRemainSeparateFromFileRecovery(t *testing.T) {
	f := newFixture(t)
	w, err := OpenWriter(f.dir, f.recipients, f.keyFile, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	blobs, sum, size, err := w.AddStream(strings.NewReader("exact historical bytes"))
	if err != nil {
		t.Fatal(err)
	}
	source := &RuntimeRecord{Format: FormatRuntime, Home: RuntimeRoot{Path: "/home/source", Resolved: "/source/home"}, OS: "linux", Arch: "arm64",
		Unresolved: []string{"runtime_dependencies_unverified"}}
	meta, err := w.Commit(Snapshot{InstanceID: "fixture", Tier: TierSoul, Runtime: source}, Files{Files: []File{{Root: "state", Path: "memory", Category: "memory", Size: size, SHA256: sum, Blobs: blobs}}}, Meta{})
	if err != nil {
		t.Fatal(err)
	}
	v, err := Open(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	s, files, err := v.Snapshot(meta.SnapshotID, f.id)
	if err != nil || s.Format != FormatSnapshot || s.Runtime == nil || s.Runtime.Home.Path != source.Home.Path || len(files.Files) != 1 {
		t.Fatal("new format/source evidence unreadable", s, err)
	}
	fixtureHeaderVersion(t, f.dir, meta.SnapshotID, f.id, func(s *Snapshot) { s.Format = FormatSnapshotLegacy; s.Runtime = nil })
	legacy, _, err := v.Snapshot(meta.SnapshotID, f.id)
	if err != nil || legacy.Runtime != nil || legacy.Format != FormatSnapshotLegacy {
		t.Fatal("legacy backup was rejected or runtime guessed", err)
	}
	fixtureHeaderVersion(t, f.dir, meta.SnapshotID, f.id, func(s *Snapshot) { s.Runtime = source })
	if head, err := v.SnapshotHeader(meta.SnapshotID, f.id); err == nil || head != nil {
		t.Fatal("runtime was smuggled under a legacy format label")
	}
	fixtureHeaderVersion(t, f.dir, meta.SnapshotID, f.id, func(s *Snapshot) { s.Format = "valise-snapshot/999" })
	if head, err := v.SnapshotHeader(meta.SnapshotID, f.id); err == nil || head != nil {
		t.Fatal("future version reached recovery")
	}
}

func TestStandaloneReadsLegacyAndNewButOldToolRefusesNewBeforeWriting(t *testing.T) {
	for _, tool := range []string{"python3", "age", "zstd"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not installed")
		}
	}
	_, here, _, _ := runtime.Caller(0)
	tools := filepath.Join(filepath.Dir(here), "..", "..", "tools", "standalone-restore")
	f := newFixture(t)
	w, err := OpenWriter(f.dir, f.recipients, f.keyFile, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	blobs, sum, size, _ := w.AddStream(strings.NewReader("historical file"))
	m, err := w.Commit(Snapshot{InstanceID: "fixture", Tier: TierSoul}, Files{Files: []File{{Root: "state", Path: "memory", Category: "memory", Mode: 0o600,
		MTime: time.Unix(100, 0), Size: size, SHA256: sum, Blobs: blobs}}}, Meta{})
	if err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(t.TempDir(), "identity.txt")
	if err := os.WriteFile(key, []byte(f.id.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(script, out string) ([]byte, error) {
		return exec.Command("python3", "-I", script, "--vault", f.dir, "--identity", key, "--snapshot", m.SnapshotID, "--out", out).CombinedOutput()
	}
	assertNoFiles := func(out string) {
		t.Helper()
		if entries, err := os.ReadDir(out); err != nil && !os.IsNotExist(err) || len(entries) != 0 {
			t.Fatal("unsupported header wrote output", err)
		}
	}
	out := filepath.Join(t.TempDir(), "old-output")
	if b, err := run(filepath.Join(tools, "legacy", "restore-v1.py"), out); err == nil || !strings.Contains(string(b), "snapshot header is invalid") {
		t.Fatal("frozen v1 tool did not refuse v2", err)
	}
	assertNoFiles(out)
	for _, version := range []string{FormatSnapshot, FormatSnapshotLegacy} {
		fixtureHeaderVersion(t, f.dir, m.SnapshotID, f.id, func(s *Snapshot) { s.Format = version })
		out := filepath.Join(t.TempDir(), "current-output")
		if _, err := run(filepath.Join(tools, "restore.py"), out); err != nil {
			t.Fatal("standalone failed a supported header", version, err)
		}
		if b, err := os.ReadFile(filepath.Join(out, "state", "memory")); err != nil || string(b) != "historical file" {
			t.Fatal("standalone changed historical file content", err)
		}
	}
	fixtureHeaderVersion(t, f.dir, m.SnapshotID, f.id, func(s *Snapshot) { s.Format = "valise-snapshot/999" })
	out = filepath.Join(t.TempDir(), "future-output")
	if _, err := run(filepath.Join(tools, "restore.py"), out); err == nil {
		t.Fatal("standalone accepted an unsupported future header")
	}
	assertNoFiles(out)
}
