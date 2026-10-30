package revive

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"valise/core/profile"
	"valise/core/revive"
	porterenroll "valise/porter/internal/enroll"
)

// A fixture instance (harness names in tests are fixtures). The fake
// systemctl keeps the unit state in a file.
type world struct {
	home, state, bin string
	r                *Runner
	p                profile.Profile
}

func write(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

const fakeSystemctl = `#!/bin/sh
f="$HOME/unit-state"
[ -f "$f" ] || echo active > "$f"
shift # --user
case "$1" in
is-active) cat "$f" ;;
show) echo loaded ;;
stop) echo inactive > "$f" ;;
start) if [ -f "$HOME/unit-broken" ]; then echo failed > "$f"; else echo active > "$f"; fi ;;
esac
`

func newWorld(t *testing.T) *world {
	t.Helper()
	home := t.TempDir()
	w := &world{home: home, state: filepath.Join(home, ".demo"), bin: filepath.Join(home, "bin")}
	write(t, filepath.Join(w.bin, "systemctl"), fakeSystemctl, 0o755)
	write(t, filepath.Join(w.bin, "journalctl"), "#!/bin/sh\necho fake log line\n", 0o755)
	write(t, filepath.Join(w.state, "SOUL.md"), "current soul", 0o644)
	write(t, filepath.Join(w.state, ".env"), "KEY=current", 0o600)
	write(t, filepath.Join(w.state, "memory/new.md"), "written after the snapshot", 0o644)
	write(t, filepath.Join(w.state, "agent.db-wal"), "stale wal", 0o644)
	write(t, filepath.Join(w.state, "agent.db"), "live db", 0o644)
	write(t, filepath.Join(w.state, "cache/x"), "cache", 0o644)
	write(t, filepath.Join(w.state, "runtime/bin/tool"), "body binary", 0o755)
	write(t, filepath.Join(w.state, "mystery"), "unclassified", 0o644)
	os.Symlink("SOUL.md", filepath.Join(w.state, "link"))
	w.p = profile.Profile{ID: "demo", ProfileVersion: 1,
		Paths: []profile.PathRule{
			{Root: "state", Glob: ".env", Category: "secrets"},
			{Root: "state", Glob: "SOUL.md", Category: "identity"},
			{Root: "state", Glob: "memory/**", Category: "memory"},
			{Root: "state", Glob: "agent.db", Category: "state"},
			{Root: "state", Glob: "cache/**", Category: "cache"},
			{Root: "state", Glob: "runtime/**", Category: "body"},
		},
		Consistency: &profile.Consistency{SQLite: []profile.SQLiteRef{{Root: "state", Glob: "agent.db"}}},
		Body:        &profile.Body{Paths: []profile.PathRef{{Root: "state", Glob: "runtime/**"}}},
		Health:      &profile.Health{Checks: []profile.HealthCheck{{Kind: "systemd_active"}}},
	}
	env := &porterenroll.Env{Home: home, Vars: []string{"PATH=" + w.bin + ":/usr/bin:/bin", "HOME=" + home}}
	w.r = &Runner{Env: env, Store: Store{Home: home}, Timing: HealthTiming{Grace: time.Millisecond, Interval: time.Millisecond, Timeout: 200 * time.Millisecond, Need: 2}}
	return w
}

func (w *world) plan() revive.Plan {
	return revive.Plan{OpID: revive.NewOpID(time.Now()), Mode: revive.ModeRollback, InstanceID: "demo", Tier: "soul",
		Profile: w.p, Roots: []revive.RootPlan{{ID: "state", Path: w.state}}, Carry: true,
		Body: revive.BodyPlan{Action: revive.BodyMoveDirs, Dirs: []string{"state/runtime"}}, Unit: "demo.service"}
}

type snapFile struct{ path, content string }

// stage writes the snapshot files through the tar data plane and verifies.
func (w *world) stage(t *testing.T, p revive.Plan, files []snapFile) {
	t.Helper()
	if _, err := StageBegin(context.Background(), w.r.Env, w.r.Store, p); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	var man []revive.ManifestEntry
	for _, f := range files {
		tw.WriteHeader(&tar.Header{Name: "state/" + f.path, Mode: 0o640, Size: int64(len(f.content)), ModTime: time.Unix(1000, 0), Typeflag: tar.TypeReg})
		tw.Write([]byte(f.content))
		sum := sha256.Sum256([]byte(f.content))
		man = append(man, revive.ManifestEntry{Path: "state/" + f.path, Size: int64(len(f.content)), SHA256: hex.EncodeToString(sum[:]), Category: "identity"})
	}
	tw.Close()
	if _, err := StageWrite(w.r.Store, p.OpID, &buf); err != nil {
		t.Fatal(err)
	}
	res, err := StageVerify(w.r.Store, revive.StageVerifyParams{OpID: p.OpID, Manifest: man})
	if err != nil || len(res.Problems) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
}

var snapshot = []snapFile{{"SOUL.md", "old soul"}, {"memory/old.md", "old memory"}}

func TestRollbackCarriesOutsideTier(t *testing.T) {
	w := newWorld(t)
	p := w.plan()
	w.stage(t, p, snapshot)
	res, err := w.r.Swap(context.Background(), p.OpID)
	if err != nil {
		t.Fatal(err)
	}
	if res.State != revive.StateHealthy || len(res.Prev) != 1 {
		t.Fatalf("%+v", res)
	}
	// Tier content comes from the snapshot; files of tier categories that
	// the snapshot lacks are gone (that is the rollback).
	if read(t, filepath.Join(w.state, "SOUL.md")) != "old soul" || read(t, filepath.Join(w.state, "memory/old.md")) != "old memory" {
		t.Fatal("snapshot not in place")
	}
	if _, err := os.Stat(filepath.Join(w.state, "memory/new.md")); !os.IsNotExist(err) {
		t.Fatal("memory written after the snapshot survived the rollback")
	}
	// Outside the soul tier: carried over; body moved; stale WAL dropped.
	if read(t, filepath.Join(w.state, ".env")) != "KEY=current" || read(t, filepath.Join(w.state, "cache/x")) != "cache" ||
		read(t, filepath.Join(w.state, "mystery")) != "unclassified" || read(t, filepath.Join(w.state, "runtime/bin/tool")) != "body binary" {
		t.Fatal("content outside the tier was not carried")
	}
	if st, _ := os.Stat(filepath.Join(w.state, ".env")); st.Mode().Perm() != 0o600 {
		t.Fatalf(".env mode %v", st.Mode())
	}
	if _, err := os.Lstat(filepath.Join(w.state, "agent.db-wal")); !os.IsNotExist(err) {
		t.Fatal("stale SQLite WAL carried into the restored state")
	}
	if target, _ := os.Readlink(filepath.Join(w.state, "link")); target != "SOUL.md" {
		t.Fatal("symlink not carried")
	}
	// The previous state is kept intact.
	prev := res.Prev[0]
	if read(t, filepath.Join(prev, "SOUL.md")) != "current soul" || read(t, filepath.Join(prev, "memory/new.md")) != "written after the snapshot" {
		t.Fatal("prev is not the pre-swap state")
	}
	if pend, _ := w.r.Store.Pending(); len(pend) != 0 {
		t.Fatal("journal not finished")
	}
	// prev drop removes only the leftovers.
	removed, err := PrevDrop(w.r.Store, p.OpID)
	if err != nil || len(removed) != 1 || removed[0] != prev {
		t.Fatalf("%v %v", removed, err)
	}
	if read(t, filepath.Join(w.state, "SOUL.md")) != "old soul" {
		t.Fatal("prev drop touched the live state")
	}
}

// snapshotTree records every regular file's content under dir.
func snapshotTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && info.Mode().IsRegular() {
			rel, _ := filepath.Rel(dir, p)
			out[rel] = read(t, p) + "|" + info.Mode().Perm().String()
		}
		return nil
	})
	return out
}

func TestHealthFailureRollsBackByteIdentical(t *testing.T) {
	w := newWorld(t)
	before := snapshotTree(t, w.state)
	p := w.plan()
	w.stage(t, p, snapshot)
	t.Setenv(revive.FaultEnv, "health")
	res, err := w.r.Swap(context.Background(), p.OpID)
	if err != nil {
		t.Fatal(err)
	}
	if res.State != revive.StateRolledBack || !strings.Contains(res.Logs, "fake log line") {
		t.Fatalf("%+v", res)
	}
	after := snapshotTree(t, w.state)
	if len(after) != len(before) {
		t.Fatalf("file set changed: %d vs %d", len(after), len(before))
	}
	for k, v := range before {
		if after[k] != v {
			t.Fatalf("%s changed after rollback", k)
		}
	}
	if read(t, filepath.Join(w.home, "unit-state")) != "active\n" {
		t.Fatal("service not restarted after rollback")
	}
}

func TestResumeFromLiveMoved(t *testing.T) {
	for _, action := range []string{"back", "continue"} {
		t.Run(action, func(t *testing.T) {
			w := newWorld(t)
			before := snapshotTree(t, w.state)
			p := w.plan()
			w.stage(t, p, snapshot)
			// Simulate a crash right after LIVE_MOVED.
			j, _ := w.r.Store.Load(p.OpID)
			StopUnit(context.Background(), w.r.Env, "demo.service")
			w.r.Store.Set(j, revive.StateStopped)
			w.r.carry(j)
			os.Rename(j.Roots[0].Live, j.Roots[0].Prev)
			w.r.Store.Set(j, revive.StateLiveMoved)
			j.ServiceWasActive = true
			w.r.Store.Save(j)

			res, err := w.r.Resume(context.Background(), revive.ResumeParams{OpID: p.OpID, Action: action})
			if err != nil {
				t.Fatal(err)
			}
			if action == "back" {
				if res.State != revive.StateRolledBack {
					t.Fatalf("%+v", res)
				}
				after := snapshotTree(t, w.state)
				for k, v := range before {
					if after[k] != v {
						t.Fatalf("%s changed", k)
					}
				}
			} else if res.State != revive.StateHealthy || read(t, filepath.Join(w.state, "SOUL.md")) != "old soul" {
				t.Fatalf("%+v", res)
			}
			if read(t, filepath.Join(w.home, "unit-state")) != "active\n" {
				t.Fatal("service not running")
			}
		})
	}
}

func TestStageRejectsUnsafeEntries(t *testing.T) {
	w := newWorld(t)
	p := w.plan()
	if _, err := StageBegin(context.Background(), w.r.Env, w.r.Store, p); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"state/../escape", "other/x", "state//x", "/abs", "state/a/../../x"} {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: 1, Typeflag: tar.TypeReg})
		tw.Write([]byte("x"))
		tw.Close()
		if _, err := StageWrite(w.r.Store, p.OpID, &buf); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	// A staged symlink cannot be used to write outside the stage.
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	tw.WriteHeader(&tar.Header{Name: "state/out", Typeflag: tar.TypeSymlink, Linkname: w.home})
	tw.WriteHeader(&tar.Header{Name: "state/out/pwned", Mode: 0o600, Size: 1, Typeflag: tar.TypeReg})
	tw.Write([]byte("x"))
	tw.Close()
	if _, err := StageWrite(w.r.Store, p.OpID, &buf); err == nil {
		t.Fatal("wrote through a staged symlink")
	}
	if _, err := os.Stat(filepath.Join(w.home, "pwned")); !os.IsNotExist(err) {
		t.Fatal("file escaped the stage")
	}
}

func TestStageVerifyDetectsTamper(t *testing.T) {
	w := newWorld(t)
	p := w.plan()
	w.stage(t, p, snapshot)
	j, _ := w.r.Store.Load(p.OpID)
	os.WriteFile(filepath.Join(j.Roots[0].Stage, "SOUL.md"), []byte("tampered"), 0o600)
	j.StageVerified = false
	w.r.Store.Save(j)
	sum := sha256.Sum256([]byte("old soul"))
	res, err := StageVerify(w.r.Store, revive.StageVerifyParams{OpID: p.OpID, Manifest: []revive.ManifestEntry{
		{Path: "state/SOUL.md", Size: 8, SHA256: hex.EncodeToString(sum[:])}}})
	if err != nil || len(res.Problems) < 2 { // tampered SOUL.md and memory/old.md not in manifest
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := w.r.Swap(context.Background(), p.OpID); err == nil {
		t.Fatal("swapped an unverified stage")
	}
}

func TestRefusesSymlinkedRoot(t *testing.T) {
	w := newWorld(t)
	real := filepath.Join(w.home, "slots", "b")
	os.MkdirAll(filepath.Dir(real), 0o755)
	os.Rename(w.state, real)
	os.Symlink(real, w.state)
	_, err := StageBegin(context.Background(), w.r.Env, w.r.Store, w.plan())
	if err == nil || !strings.Contains(err.Error(), "root_symlinked") {
		t.Fatal(err)
	}
}

func TestPendingOperationBlocksNewOne(t *testing.T) {
	w := newWorld(t)
	w.stage(t, w.plan(), snapshot)
	if _, err := StageBegin(context.Background(), w.r.Env, w.r.Store, w.plan()); err == nil || !strings.Contains(err.Error(), "op_pending") {
		t.Fatal(err)
	}
}
