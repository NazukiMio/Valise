package login

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"valise/core/enroll"
	"valise/core/login"
	"valise/core/profile"
	"valise/core/valise"
	porterenroll "valise/porter/internal/enroll"
)

func setupEntryTestEnv(t *testing.T, home string, prof profile.Profile) *porterenroll.Env {
	t.Helper()
	stateDir := filepath.Join(home, ".demo")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	rec := enroll.Record{
		InstanceID:     "demo",
		ProfileID:      "demo",
		ProfileVersion: 1,
		Roots:          map[string]string{"state": stateDir},
		Service:        []enroll.ServiceState{{Scope: "user", Unit: "demo.service", LoadState: "loaded", Active: "active"}},
	}
	if _, err := porterenroll.WriteRecord(home, rec, prof); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(home, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	return &porterenroll.Env{
		Home: home,
		Vars: []string{
			"HOME=" + home,
			"PATH=" + binDir + ":/usr/bin:/bin",
		},
	}
}

func writeFakeProcNet(t *testing.T, dir string, port int) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	content := "  sl  local_address rem_address   st\n"
	if port > 0 {
		content += fmt.Sprintf("   0: 0100007F:%04X 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 1\n", port)
	}
	if err := os.WriteFile(filepath.Join(dir, "tcp"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tcp6"), []byte("  sl  local_address rem_address   st\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestEntryLifecycleProbePrepareStartStop(t *testing.T) {
	home := t.TempDir()
	procDir := filepath.Join(home, "procnet")
	oldProc := ProcNet
	oldPoll := entryStartPoll
	ProcNet = procDir
	entryStartPoll = 50 * time.Millisecond
	t.Cleanup(func() {
		ProcNet = oldProc
		entryStartPoll = oldPoll
	})
	writeFakeProcNet(t, procDir, 0)

	prof := profile.Profile{
		Schema:         profile.Schema,
		ID:             "demo",
		DisplayName:    "Demo",
		ProfileVersion: 1,
		Status:         profile.StatusDraft,
		Service: &profile.Service{
			Candidates: []profile.ServiceUnit{{Scope: "user", Unit: "demo.service"}},
		},
	}
	env := setupEntryTestEnv(t, home, prof)

	// Stub demo CLI and systemctl.
	binDir := filepath.Join(home, "bin")
	demoBin := filepath.Join(binDir, "demo")
	demoScript := fmt.Sprintf(`#!/bin/sh
case "$1" in
check)
  if [ -f "$HOME/.demo/enabled" ]; then
    echo "true"
  else
    echo "false"
  fi
  ;;
enable)
  mkdir -p "$HOME/.demo/dist"
  touch "$HOME/.demo/dist/index.html"
  echo "true" > "$HOME/.demo/enabled"
  ;;
serve)
  printf '  sl  local_address rem_address   st\n   0: 0100007F:%04X 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 1\n' > %q
  sleep 30
  ;;
stop-serve)
  printf '  sl  local_address rem_address   st\n' > %q
  ;;
esac
`, 9120, filepath.Join(procDir, "tcp"), filepath.Join(procDir, "tcp"))
	if err := os.WriteFile(demoBin, []byte(demoScript), 0o700); err != nil {
		t.Fatal(err)
	}
	systemctlBin := filepath.Join(binDir, "systemctl")
	systemctlScript := `#!/bin/sh
echo "$@" >> "$HOME/systemctl.log"
exit 0
`
	if err := os.WriteFile(systemctlBin, []byte(systemctlScript), 0o700); err != nil {
		t.Fatal(err)
	}

	entry := profile.Entry{
		ID:          "webapp",
		DisplayName: "WebApp",
		Kind:        "web",
		Host:        "127.0.0.1",
		Port:        9120,
		Path:        "/",
		Prepare: &profile.EntryPrepare{
			Summary:        "enable webapp",
			CheckPaths:     []string{"dist/index.html"},
			CheckArgv:      profile.Argv{"demo", "check"},
			ExpectOutput:   "true",
			Steps:          []profile.Argv{{"demo", "enable"}},
			RestartService: true,
		},
		Start: &profile.EntryStart{
			Summary:     "start webapp",
			Argv:        profile.Argv{"demo", "serve"},
			StopArgv:    profile.Argv{"demo", "stop-serve"},
			WaitSeconds: 3,
		},
	}

	ctx := context.Background()
	// 1. Initial probe: not listening, not prepared.
	probe, err := ProbeEntry(ctx, env, home, login.EntryProbeParams{InstanceID: "demo", Entry: entry})
	if err != nil {
		t.Fatalf("probe1: %v", err)
	}
	if probe.Listening || !probe.HasPrepare || probe.Prepared || !probe.HasStart || probe.ManagedByUs {
		t.Fatalf("unexpected probe1: %+v", probe)
	}

	// 2. Prepare: runs steps & restarts service.
	prep, err := PrepareEntry(ctx, env, home, login.EntryPrepareParams{InstanceID: "demo", Entry: entry})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if prep.StepsRun != 1 || !prep.ServiceRestarted || !prep.Prepared {
		t.Fatalf("unexpected prepare result: %+v", prep)
	}

	// 3. Start: launches detached (Setsid fallback since no systemd-run in bin), detects listening.
	started, err := StartEntry(ctx, env, home, login.EntryStartParams{InstanceID: "demo", Entry: entry})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if !started.Listening || !started.ManagedByUs || started.ManagedPID <= 0 || started.LogPath == "" {
		t.Fatalf("unexpected start result: %+v", started)
	}
	// Clean up child if test fails early.
	t.Cleanup(func() {
		_, _ = StopEntry(context.Background(), env, home, login.EntryStopParams{InstanceID: "demo", Entry: entry})
	})

	// 4. Probe while running.
	probe2, err := ProbeEntry(ctx, env, home, login.EntryProbeParams{InstanceID: "demo", Entry: entry})
	if err != nil {
		t.Fatalf("probe2: %v", err)
	}
	if !probe2.Listening || !probe2.Prepared || !probe2.ManagedByUs || !probe2.ManagedAlive {
		t.Fatalf("unexpected probe2: %+v", probe2)
	}

	// 5. Stop: kills managed process & cleans state file.
	stopped, err := StopEntry(ctx, env, home, login.EntryStopParams{InstanceID: "demo", Entry: entry})
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if !stopped.Stopped || stopped.Listening {
		t.Fatalf("unexpected stop result: %+v", stopped)
	}
	if _, err := os.Stat(entryStatePath(home, "demo", "webapp")); !os.IsNotExist(err) {
		t.Fatalf("state file not removed: %v", err)
	}
}

func writeFakeProcStat(t *testing.T, root string, pid int, startTicks string) {
	t.Helper()
	dir := filepath.Join(root, strconv.Itoa(pid))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Fields 3..22 after "(comm) ": 19 dummy fields + field 22 (startTicks).
	stat := fmt.Sprintf("%d (demo proc) S 1 1 1 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 %s 0\n", pid, startTicks)
	if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestEntryLifecycleStaleStateCleanupAndStopGuard(t *testing.T) {
	home := t.TempDir()
	procNetDir := filepath.Join(home, "procnet")
	fakeProc := filepath.Join(home, "fakeproc")
	if err := os.MkdirAll(fakeProc, 0o700); err != nil {
		t.Fatal(err)
	}
	oldProcNet, oldProcRoot := ProcNet, procRoot
	ProcNet, procRoot = procNetDir, fakeProc
	t.Cleanup(func() {
		ProcNet, procRoot = oldProcNet, oldProcRoot
	})
	writeFakeProcNet(t, procNetDir, 9120)

	prof := profile.Profile{
		Schema:         profile.Schema,
		ID:             "demo",
		DisplayName:    "Demo",
		ProfileVersion: 1,
		Status:         profile.StatusDraft,
	}
	env := setupEntryTestEnv(t, home, prof)

	stopMarker := filepath.Join(home, "stop-called")
	binDir := filepath.Join(home, "bin")
	if err := os.WriteFile(filepath.Join(binDir, "demo"), []byte(fmt.Sprintf("#!/bin/sh\ntouch %q\n", stopMarker)), 0o700); err != nil {
		t.Fatal(err)
	}

	entry := profile.Entry{
		ID: "webapp", Kind: "web", Host: "127.0.0.1", Port: 9120, Path: "/",
		Start: &profile.EntryStart{
			Argv:     profile.Argv{"demo", "serve"},
			StopArgv: profile.Argv{"demo", "stop-serve"},
		},
	}

	// 1. StopEntry with st == nil must NOT run StopArgv.
	ctx := context.Background()
	if _, err := StopEntry(ctx, env, home, login.EntryStopParams{InstanceID: "demo", Entry: entry}); err != nil {
		t.Fatalf("StopEntry(nil state): %v", err)
	}
	if _, err := os.Stat(stopMarker); !os.IsNotExist(err) {
		t.Fatal("StopEntry ran StopArgv when no managed entryState existed")
	}

	// 2. isManagedPIDAlive checks ProcStart and never treats EPERM or mismatched ProcStart as alive.
	selfPID := os.Getpid()
	writeFakeProcStat(t, fakeProc, selfPID, "12345")
	if !isManagedPIDAlive(&entryState{PID: selfPID, ProcStart: "12345"}) {
		t.Fatal("expected matching ProcStart to be alive")
	}
	if isManagedPIDAlive(&entryState{PID: selfPID, ProcStart: "99999"}) {
		t.Fatal("mismatched ProcStart must not be treated as alive")
	}
	if isManagedPIDAlive(&entryState{PID: selfPID, ProcStart: ""}) {
		t.Fatal("empty ProcStart with procfs present must not be treated as alive")
	}
	if os.Geteuid() != 0 && isManagedPIDAlive(&entryState{PID: 1, ProcStart: "1"}) {
		t.Fatal("EPERM on PID 1 must not be treated as alive")
	}

	// 3. Stale state file (wrong ProcStart pointing to a live PID): ProbeEntry deletes it and reports not managed.
	if err := writeEntryState(home, entryState{
		InstanceID: "demo", EntryID: "webapp", Port: 9120,
		PID: selfPID, PGID: selfPID, ProcStart: "99999",
	}); err != nil {
		t.Fatal(err)
	}
	probe, err := ProbeEntry(ctx, env, home, login.EntryProbeParams{InstanceID: "demo", Entry: entry})
	if err != nil {
		t.Fatalf("ProbeEntry stale: %v", err)
	}
	if probe.ManagedByUs || probe.ManagedAlive {
		t.Fatalf("expected stale state to report ManagedByUs=false ManagedAlive=false, got %+v", probe)
	}
	if _, err := os.Stat(entryStatePath(home, "demo", "webapp")); !os.IsNotExist(err) {
		t.Fatal("ProbeEntry did not delete stale state file")
	}

	// 4. Stale state file on AlreadyUp StartEntry: deletes stale file and reports ManagedByUs=false.
	if err := writeEntryState(home, entryState{
		InstanceID: "demo", EntryID: "webapp", Port: 9120,
		PID: selfPID, PGID: selfPID, ProcStart: "99999",
	}); err != nil {
		t.Fatal(err)
	}
	stRes, err := StartEntry(ctx, env, home, login.EntryStartParams{InstanceID: "demo", Entry: entry})
	if err != nil {
		t.Fatalf("StartEntry AlreadyUp stale: %v", err)
	}
	if !stRes.AlreadyUp || stRes.ManagedByUs {
		t.Fatalf("expected AlreadyUp=true ManagedByUs=false, got %+v", stRes)
	}
	if _, err := os.Stat(entryStatePath(home, "demo", "webapp")); !os.IsNotExist(err) {
		t.Fatal("StartEntry did not delete stale state file")
	}

	// 5. Stale state file (mismatched ProcStart) on StopEntry: must NOT kill selfPID or run StopArgv, and must delete state file.
	if err := writeEntryState(home, entryState{
		InstanceID: "demo", EntryID: "webapp", Port: 9120,
		PID: selfPID, PGID: selfPID, ProcStart: "99999",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := StopEntry(ctx, env, home, login.EntryStopParams{InstanceID: "demo", Entry: entry}); err != nil {
		t.Fatalf("StopEntry stale: %v", err)
	}
	if _, err := os.Stat(stopMarker); !os.IsNotExist(err) {
		t.Fatal("StopEntry ran StopArgv for stale mismatched ProcStart")
	}
	if _, err := os.Stat(entryStatePath(home, "demo", "webapp")); !os.IsNotExist(err) {
		t.Fatal("StopEntry did not remove stale state file")
	}
}

func TestEntryLifecycleSecurityAuditAndNoOutputLeak(t *testing.T) {
	home := t.TempDir()
	procNetDir := filepath.Join(home, "procnet")
	oldProcNet := ProcNet
	ProcNet = procNetDir
	t.Cleanup(func() { ProcNet = oldProcNet })
	writeFakeProcNet(t, procNetDir, 0)

	prof := profile.Profile{
		Schema: profile.Schema, ID: "demo", DisplayName: "Demo", ProfileVersion: 1, Status: profile.StatusDraft,
	}
	env := setupEntryTestEnv(t, home, prof)
	env.Set("SECRET", "top-secret-token")

	binDir := filepath.Join(home, "bin")
	if err := os.WriteFile(filepath.Join(binDir, "demo"), []byte(`#!/bin/sh
case "$1" in
check-secret)
  echo "SUPER_SECRET_STDOUT_VALUE"
  exit 0
  ;;
fail-secret)
  echo "SUPER_SECRET_STDOUT_FAIL"
  echo "SUPER_SECRET_STDERR_FAIL" >&2
  exit 2
  ;;
esac
`), 0o700); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	// 1. Reject sh -c and ${env:SECRET}.
	for _, bad := range []profile.Argv{
		{"sh", "-c", "echo pwned"},
		{"demo", "${env:SECRET}"},
	} {
		_, err := PrepareEntry(ctx, env, home, login.EntryPrepareParams{
			InstanceID: "demo",
			Entry: profile.Entry{
				ID: "webapp", Kind: "web", Host: "127.0.0.1", Port: 9120, Path: "/",
				Prepare: &profile.EntryPrepare{Steps: []profile.Argv{bad}},
			},
		})
		if ue, ok := valise.As(err); !ok || ue.Code != "entry_argv_invalid" {
			t.Fatalf("PrepareEntry(%v) expected entry_argv_invalid, got %v", bad, err)
		}
		_, err = StartEntry(ctx, env, home, login.EntryStartParams{
			InstanceID: "demo",
			Entry: profile.Entry{
				ID: "webapp", Kind: "web", Host: "127.0.0.1", Port: 9120, Path: "/",
				Start: &profile.EntryStart{Argv: bad},
			},
		})
		if ue, ok := valise.As(err); !ok || ue.Code != "entry_argv_invalid" {
			t.Fatalf("StartEntry(%v) expected entry_argv_invalid, got %v", bad, err)
		}
	}

	// 2. evaluatePrepare mismatch must NOT echo raw stdout.
	probe, err := ProbeEntry(ctx, env, home, login.EntryProbeParams{
		InstanceID: "demo",
		Entry: profile.Entry{
			ID: "webapp", Kind: "web", Host: "127.0.0.1", Port: 9120, Path: "/",
			Prepare: &profile.EntryPrepare{
				CheckArgv:    profile.Argv{"demo", "check-secret"},
				ExpectOutput: "true",
				Steps:        []profile.Argv{{"demo", "fail-secret"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("ProbeEntry: %v", err)
	}
	if probe.Prepared {
		t.Fatal("expected Prepared=false")
	}
	if strings.Contains(probe.PrepareNote, "SUPER_SECRET_STDOUT_VALUE") {
		t.Fatalf("PrepareNote leaked raw stdout: %q", probe.PrepareNote)
	}

	// 3. PrepareEntry failure must NOT echo raw stdout or stderr.
	_, err = PrepareEntry(ctx, env, home, login.EntryPrepareParams{
		InstanceID: "demo",
		Entry: profile.Entry{
			ID: "webapp", Kind: "web", Host: "127.0.0.1", Port: 9120, Path: "/",
			Prepare: &profile.EntryPrepare{
				Steps: []profile.Argv{{"demo", "fail-secret"}},
			},
		},
	})
	if err == nil {
		t.Fatal("expected PrepareEntry failure")
	}
	ue, ok := valise.As(err)
	if !ok || ue.Code != "entry_prepare_failed" {
		t.Fatalf("expected entry_prepare_failed, got %v", err)
	}
	if strings.Contains(err.Error(), "SUPER_SECRET") || strings.Contains(ue.Detail, "SUPER_SECRET") {
		t.Fatalf("PrepareEntry error leaked raw output: err=%q detail=%q", err.Error(), ue.Detail)
	}
}
