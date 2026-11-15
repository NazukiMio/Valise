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

	"valise/core/enroll"
	"valise/core/profile"
	"valise/core/revive"
	"valise/core/valise"
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
	if err := os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
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

func TestIntruderIsMovedAsideOnBack(t *testing.T) {
	w := newWorld(t)
	p := w.plan()
	w.stage(t, p, snapshot)
	j, _ := w.r.Store.Load(p.OpID)
	StopUnit(context.Background(), w.r.Env, "demo.service")
	j.ServiceWasActive = true
	w.r.Store.Set(j, revive.StateStopped)
	os.Rename(j.Roots[0].Live, j.Roots[0].Prev)
	w.r.Store.Set(j, revive.StateLiveMoved)
	// Another process of the harness recreates the live path by writing to it.
	write(t, filepath.Join(w.state, "shared-state.db"), "written during the crash", 0o644)

	res, err := w.r.Resume(context.Background(), revive.ResumeParams{OpID: p.OpID, Action: "back"})
	if err != nil || res.State != revive.StateRolledBack || len(res.Intruders) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if read(t, filepath.Join(w.state, "SOUL.md")) != "current soul" {
		t.Fatal("pre-swap state not restored")
	}
	if read(t, filepath.Join(res.Intruders[0], "shared-state.db")) != "written during the crash" {
		t.Fatal("intruder content not kept")
	}
	// prev drop also lists the intruder for removal.
	if removed, _ := PrevDrop(w.r.Store, p.OpID); len(removed) == 0 || removed[0] != res.Intruders[0] {
		t.Fatalf("%v", removed)
	}
}

func TestBusyProcessesFromProc(t *testing.T) {
	fake := t.TempDir()
	old := procRoot
	procRoot = fake
	t.Cleanup(func() { procRoot = old })
	mk := func(pid, cwd, fd, cgroup, cmd string) {
		d := filepath.Join(fake, pid)
		os.MkdirAll(filepath.Join(d, "fd"), 0o755)
		os.Symlink(cwd, filepath.Join(d, "cwd"))
		if fd != "" {
			os.Symlink(fd, filepath.Join(d, "fd", "3"))
		}
		os.WriteFile(filepath.Join(d, "cgroup"), []byte(cgroup), 0o644)
		os.WriteFile(filepath.Join(d, "cmdline"), []byte(cmd), 0o644)
	}
	mk("100", "/home/u", "/home/u/.demo/shared.db", "0::/user.slice/app.slice/other.scope", "dashboard\x00--port\x009119")
	mk("101", "/home/u/.demo", "", "0::/user.slice/app.slice/demo.service", "gateway")
	mk("102", "/tmp", "/home/u/.demo-other/x", "0::/", "unrelated")
	got := busyProcesses([]string{"/home/u/.demo"}, "demo.service")
	if len(got) != 1 || got[0].PID != 100 || got[0].Command != "dashboard --port 9119" {
		t.Fatalf("%+v", got)
	}
	if got := busyProcesses([]string{"/home/u/.demo"}, ""); len(got) != 2 {
		t.Fatalf("unit processes must count once the unit should be stopped: %+v", got)
	}
}

func TestCloneSupersedesOverlappingInstance(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	old := enroll.Record{InstanceID: "old", ProfileID: "demo", ProfileVersion: 1, Roots: map[string]string{"state": w.state}}
	if _, err := porterenroll.WriteRecord(w.home, old, w.p); err != nil {
		t.Fatal(err)
	}
	roots := []revive.RootPlan{{ID: "state", Path: w.state}}
	facts, err := Inspect(ctx, w.r.Env, w.r.Store, revive.InspectParams{Profile: w.p, InstanceID: "new", Roots: roots})
	if err != nil || len(facts.Overlapping) != 1 || facts.Overlapping[0] != "old" {
		t.Fatalf("overlapping = %v, %v", facts.Overlapping, err)
	}

	p := revive.Plan{OpID: revive.NewOpID(time.Now()), Mode: revive.ModeClone, InstanceID: "new", Tier: "full",
		Profile: w.p, Roots: roots, Body: revive.BodyPlan{Action: revive.BodyKeep}, Unit: "demo.service",
		Record:     &enroll.Record{InstanceID: "new", ProfileID: "demo", ProfileVersion: 1, Roots: map[string]string{"state": w.state}},
		Supersedes: facts.Overlapping}
	w.stage(t, p, snapshot)
	res, err := w.r.Swap(ctx, p.OpID)
	if err != nil || res.State != revive.StateHealthy {
		t.Fatalf("%+v %v", res, err)
	}
	rec, _ := porterenroll.ReadRecord(w.home, "old")
	if rec == nil || rec.Superseded == nil || rec.Superseded.By != "new" || rec.Superseded.OpID != p.OpID {
		t.Fatalf("old record not marked: %+v", rec)
	}
	// A superseded instance no longer overlaps (it is not replaced twice).
	facts, _ = Inspect(ctx, w.r.Env, w.r.Store, revive.InspectParams{Profile: w.p, InstanceID: "other", Roots: roots})
	if len(facts.Overlapping) != 1 || facts.Overlapping[0] != "new" {
		t.Fatalf("overlapping after supersede = %v", facts.Overlapping)
	}

	list, err := OldList(w.r.Store)
	if err != nil || len(list.Entries) != 1 || list.Entries[0].InstanceID != "old" ||
		len(list.Entries[0].Leftovers) != 1 || list.Entries[0].Leftovers[0].Path != res.Prev[0] {
		t.Fatalf("%+v %v", list, err)
	}
	if _, err := OldCleanup(w.r.Store, "new"); err == nil {
		t.Fatal("cleanup of a live instance accepted")
	}
	removed, err := OldCleanup(w.r.Store, "old")
	if err != nil || len(removed) != 2 {
		t.Fatalf("%v %v", removed, err)
	}
	if _, err := os.Stat(res.Prev[0]); !os.IsNotExist(err) {
		t.Fatal("kept directory survived cleanup")
	}
	if r, _ := porterenroll.ReadRecord(w.home, "old"); r != nil {
		t.Fatal("old record survived cleanup")
	}
	if r, _ := porterenroll.ReadRecord(w.home, "new"); r == nil || read(t, filepath.Join(w.state, "SOUL.md")) != "old soul" {
		t.Fatal("cleanup touched the live instance")
	}
}

// A state dir with another agent inside (a named instance) is never
// swapped: the swap would carry that agent off while it runs. The stage
// can still be discarded (restore test).
func TestSwapRefusesNestedInstance(t *testing.T) {
	w := newWorld(t)
	w.p.Detect = &profile.Detect{StateDirCandidates: []string{"${HOME}/.demo"}, MarkersAny: []string{"SOUL.md"},
		Named: []profile.Named{{Glob: "${HOME}/.demo/profiles/*", Env: map[string]string{"DEMO_HOME": "${STATE_DIR}"}}}}
	write(t, filepath.Join(w.state, "profiles/bot/SOUL.md"), "another agent", 0o644)
	p := w.plan()
	w.stage(t, p, snapshot)
	_, err := w.r.Swap(context.Background(), p.OpID)
	if ue, ok := valise.As(err); !ok || ue.Code != "nested_instances" {
		t.Fatalf("swapped over a nested agent: %v", err)
	}
	if read(t, filepath.Join(w.state, "SOUL.md")) != "current soul" || read(t, filepath.Join(w.state, "profiles/bot/SOUL.md")) != "another agent" {
		t.Fatal("live state changed")
	}
	if _, err := w.r.Resume(context.Background(), revive.ResumeParams{OpID: p.OpID, Action: "discard"}); err != nil {
		t.Fatal(err)
	}
}

// Named instances running under the same service are carried over: their
// directories move into the new live directory and back on a rollback.
func TestSwapCarriesNestedSharedInstance(t *testing.T) {
	for _, broken := range []bool{false, true} {
		w := newWorld(t)
		w.p.Detect = &profile.Detect{StateDirCandidates: []string{"${HOME}/.demo"}, MarkersAny: []string{"SOUL.md"},
			Named: []profile.Named{{Glob: "${HOME}/.demo/profiles/*", Env: map[string]string{"DEMO_HOME": "${STATE_DIR}"}, Service: "shared"}}}
		write(t, filepath.Join(w.state, "profiles/bot/SOUL.md"), "another agent", 0o644)
		write(t, filepath.Join(w.state, "profiles/bot/memory/m.md"), "its memory", 0o644)
		if broken {
			write(t, filepath.Join(w.home, "unit-broken"), "", 0o644)
		}
		p := w.plan()
		p.Keep = []string{"state/profiles/bot"}
		w.stage(t, p, snapshot)
		res, err := w.r.Swap(context.Background(), p.OpID)
		if err != nil {
			t.Fatal(err)
		}
		want := "old soul"
		if broken {
			want = "current soul"
			if res.State != revive.StateRolledBack {
				t.Fatalf("%+v", res)
			}
		}
		if read(t, filepath.Join(w.state, "SOUL.md")) != want {
			t.Fatalf("broken=%v: soul %q", broken, read(t, filepath.Join(w.state, "SOUL.md")))
		}
		if read(t, filepath.Join(w.state, "profiles/bot/SOUL.md")) != "another agent" || read(t, filepath.Join(w.state, "profiles/bot/memory/m.md")) != "its memory" {
			t.Fatalf("broken=%v: the nested agent was not carried over", broken)
		}
	}
}

// A planned root inside another instance's root supersedes nothing: at the
// profile's named-instance location it is the normal nested layout and
// passes; anywhere else it is listed as inside and refused by the plan
// (docs/ARCHITECTURE.md 8.2).
func TestCloneIntoAnotherInstanceDir(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	w.p.Detect = &profile.Detect{StateDirCandidates: []string{"${HOME}/.demo"}, MarkersAny: []string{"SOUL.md"},
		Named: []profile.Named{{Glob: "${HOME}/.demo/profiles/*", Service: "shared"}}}
	def := enroll.Record{InstanceID: "demo", ProfileID: "demo", ProfileVersion: 1, Roots: map[string]string{"state": w.state}}
	if _, err := porterenroll.WriteRecord(w.home, def, w.p); err != nil {
		t.Fatal(err)
	}
	named := []revive.RootPlan{{ID: "state", Path: filepath.Join(w.state, "profiles", "copy")}}
	facts, err := Inspect(ctx, w.r.Env, w.r.Store, revive.InspectParams{Profile: w.p, InstanceID: "demo-copy", Roots: named})
	if err != nil || len(facts.Overlapping) != 0 || len(facts.Inside) != 0 {
		t.Fatalf("named location: overlapping %v inside %v %v", facts.Overlapping, facts.Inside, err)
	}
	elsewhere := []revive.RootPlan{{ID: "state", Path: filepath.Join(w.state, "cache", "copy")}}
	facts, err = Inspect(ctx, w.r.Env, w.r.Store, revive.InspectParams{Profile: w.p, InstanceID: "demo-copy", Roots: elsewhere})
	if err != nil || len(facts.Overlapping) != 0 || len(facts.Inside) != 1 || facts.Inside[0] != "demo" {
		t.Fatalf("elsewhere: overlapping %v inside %v %v", facts.Overlapping, facts.Inside, err)
	}
}

// A kept body inside a replaced root travels into the new live directory in
// a migration, comes back on failure, and is skipped when the target never
// had it (docs/ARCHITECTURE.md 8.2.1, body nested in the state dir).
func TestMigrateCarriesKeptNestedBody(t *testing.T) {
	for _, tc := range []struct {
		name           string
		broken, noBody bool
	}{{"healthy", false, false}, {"rolled back", true, false}, {"no body on target", false, true}} {
		w := newWorld(t)
		if tc.noBody {
			os.RemoveAll(filepath.Join(w.state, "runtime"))
		}
		if tc.broken {
			write(t, filepath.Join(w.home, "unit-broken"), "", 0o644)
		}
		p := w.plan()
		p.Mode, p.Carry = revive.ModeMigrate, false
		p.Body = revive.BodyPlan{Action: revive.BodyKeep, Dirs: []string{"state/runtime"}}
		w.stage(t, p, snapshot)
		res, err := w.r.Swap(context.Background(), p.OpID)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		wantState, wantSoul := revive.StateHealthy, "old soul"
		if tc.broken {
			wantState, wantSoul = revive.StateRolledBack, "current soul"
		}
		if res.State != wantState || read(t, filepath.Join(w.state, "SOUL.md")) != wantSoul {
			t.Fatalf("%s: %+v soul %q", tc.name, res, read(t, filepath.Join(w.state, "SOUL.md")))
		}
		_, err = os.Stat(filepath.Join(w.state, "runtime/bin/tool"))
		if tc.noBody != os.IsNotExist(err) {
			t.Fatalf("%s: body present=%v", tc.name, err == nil)
		}
		if !tc.broken && exists(filepath.Join(w.state, ".env")) {
			t.Fatalf("%s: a migration carried the target's credentials", tc.name)
		}
	}
}
