package login

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
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

func TestPrepareEntryWaitsForUnitActiveAfterRestart(t *testing.T) {
	home := t.TempDir()
	procDir := filepath.Join(home, "procnet")
	oldProc, oldUnitPoll := ProcNet, unitActivePoll
	ProcNet, unitActivePoll = procDir, 20*time.Millisecond
	t.Cleanup(func() {
		ProcNet, unitActivePoll = oldProc, oldUnitPoll
	})
	writeFakeProcNet(t, procDir, 0)

	prof := profile.Profile{
		Schema: profile.Schema, ID: "demo", DisplayName: "Demo", ProfileVersion: 1, Status: profile.StatusDraft,
		Service: &profile.Service{Candidates: []profile.ServiceUnit{{Scope: "user", Unit: "demo.service"}}},
		Health:  &profile.Health{GraceSeconds: 5},
	}
	env := setupEntryTestEnv(t, home, prof)
	binDir := filepath.Join(home, "bin")

	// systemctl returns "activating" twice, then "active" and creates the ready marker.
	systemctlScript := `#!/bin/sh
if [ "$2" = "is-active" ]; then
  count=0
  if [ -f "$HOME/is_active_count" ]; then
    count=$(cat "$HOME/is_active_count")
  fi
  count=$((count + 1))
  echo "$count" > "$HOME/is_active_count"
  if [ "$count" -lt 3 ]; then
    echo "activating"
    exit 3
  fi
  touch "$HOME/.demo/unit_active"
  echo "active"
  exit 0
fi
exit 0
`
	if err := os.WriteFile(filepath.Join(binDir, "systemctl"), []byte(systemctlScript), 0o700); err != nil {
		t.Fatal(err)
	}
	// check only returns "true" once unit_active marker exists.
	demoScript := `#!/bin/sh
case "$1" in
enable) exit 0 ;;
check)
  if [ -f "$HOME/.demo/unit_active" ]; then
    echo "true"
  else
    echo "false"
  fi
  ;;
esac
`
	if err := os.WriteFile(filepath.Join(binDir, "demo"), []byte(demoScript), 0o700); err != nil {
		t.Fatal(err)
	}

	entry := profile.Entry{
		ID: "webapp", Kind: "web", Host: "127.0.0.1", Port: 9120, Path: "/",
		Prepare: &profile.EntryPrepare{
			CheckArgv:      profile.Argv{"demo", "check"},
			ExpectOutput:   "true",
			Steps:          []profile.Argv{{"demo", "enable"}},
			RestartService: true,
		},
	}
	res, err := PrepareEntry(context.Background(), env, home, login.EntryPrepareParams{InstanceID: "demo", Entry: entry})
	if err != nil {
		t.Fatalf("PrepareEntry: %v", err)
	}
	if !res.ServiceRestarted || !res.Prepared {
		t.Fatalf("expected ServiceRestarted=true and Prepared=true after unit active wait, got %+v", res)
	}
}

func TestStartEntryHTTPReadinessAndCaptureURLRedaction(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	port, err := strconv.Atoi(srv.URL[strings.LastIndex(srv.URL, ":")+1:])
	if err != nil {
		t.Fatal(err)
	}

	home := t.TempDir()
	procDir := filepath.Join(home, "procnet")
	oldProc, oldPoll := ProcNet, entryStartPoll
	ProcNet, entryStartPoll = procDir, 30*time.Millisecond
	t.Cleanup(func() {
		ProcNet, entryStartPoll = oldProc, oldPoll
	})
	writeFakeProcNet(t, procDir, 0)

	prof := profile.Profile{
		Schema: profile.Schema, ID: "demo", DisplayName: "Demo", ProfileVersion: 1, Status: profile.StatusDraft,
	}
	env := setupEntryTestEnv(t, home, prof)
	binDir := filepath.Join(home, "bin")

	// Setsid path: verifies PYTHONUNBUFFERED=1, prints startup log with one-time
	// token URL, writes fake procnet, then simulates post-start HTTP access log
	// containing a plaintext token written to its inherited stdout FD.
	envMarker := filepath.Join(home, "pyunbuf.txt")
	afterMarker := filepath.Join(home, "after-write.txt")
	demoScript := fmt.Sprintf(`#!/bin/sh
printf '%%s' "$PYTHONUNBUFFERED" > %q
echo "Booting webapp..."
echo "Open http://127.0.0.1:%d/?token=super-secret-tok-123"
echo "token=should-also-be-redacted"
printf '  sl  local_address rem_address   st\n   0: 0100007F:%04X 00000000:0000 0A 00000000:00000000 00000000 00000000  1000        0 1\n' > %q
sleep 0.25
echo "GET /?token=post-start-access-log-secret HTTP/1.1 200"
echo "done" > %q
sleep 30
`, envMarker, port, port, filepath.Join(procDir, "tcp"), afterMarker)
	if err := os.WriteFile(filepath.Join(binDir, "demo"), []byte(demoScript), 0o700); err != nil {
		t.Fatal(err)
	}

	entry := profile.Entry{
		ID: "webapp", Kind: "web", Host: "127.0.0.1", Port: port, Path: "/",
		Start: &profile.EntryStart{
			Argv:        profile.Argv{"demo", "serve"},
			WaitSeconds: 5,
			CaptureURL:  true,
		},
	}

	res, err := StartEntry(context.Background(), env, home, login.EntryStartParams{InstanceID: "demo", Entry: entry})
	if err != nil {
		t.Fatalf("StartEntry: %v", err)
	}
	t.Cleanup(func() {
		_, _ = StopEntry(context.Background(), env, home, login.EntryStopParams{InstanceID: "demo", Entry: entry})
	})

	if !res.Listening {
		t.Fatalf("expected Listening=true, got %+v", res)
	}
	if hits.Load() == 0 {
		t.Fatal("expected HTTP readiness probe to hit loopback server")
	}
	if res.AuthSuffix != "/?token=super-secret-tok-123" {
		t.Fatalf("AuthSuffix = %q, want %q", res.AuthSuffix, "/?token=super-secret-tok-123")
	}
	if pyVal, err := os.ReadFile(envMarker); err != nil || string(pyVal) != "1" {
		t.Fatalf("expected PYTHONUNBUFFERED=1 in Setsid child env, got %q (err=%v)", string(pyVal), err)
	}
	// Wait until the detached child has written its post-start access log to the old FD.
	waitDone := time.Now().Add(2 * time.Second)
	for time.Now().Before(waitDone) {
		if _, err := os.Stat(afterMarker); err == nil {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if _, err := os.Stat(afterMarker); err != nil {
		t.Fatalf("detached child did not complete post-start write: %v", err)
	}
	logBytes, err := os.ReadFile(res.LogPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	logText := string(logBytes)
	if strings.Contains(logText, "super-secret-tok-123") ||
		strings.Contains(logText, "post-start-access-log-secret") ||
		strings.Contains(logText, "http://") ||
		strings.Contains(strings.ToLower(logText), "token") {
		t.Fatalf("log file was not sanitized or still received child writes: %q", logText)
	}
	if !strings.Contains(logText, "Booting webapp...") || !strings.Contains(logText, "[REDACTED]") {
		t.Fatalf("unexpected sanitized log content: %q", logText)
	}
	fi, err := os.Stat(res.LogPath)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("expected 0600 log permissions, got %v (err=%v)", fi.Mode().Perm(), err)
	}
}

func TestStartEntryCaptureURLSystemdRunPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	port, err := strconv.Atoi(srv.URL[strings.LastIndex(srv.URL, ":")+1:])
	if err != nil {
		t.Fatal(err)
	}

	home := t.TempDir()
	procDir := filepath.Join(home, "procnet")
	oldProc, oldPoll := ProcNet, entryStartPoll
	ProcNet, entryStartPoll = procDir, 30*time.Millisecond
	t.Cleanup(func() {
		ProcNet, entryStartPoll = oldProc, oldPoll
	})
	writeFakeProcNet(t, procDir, 0)

	prof := profile.Profile{
		Schema: profile.Schema, ID: "demo", DisplayName: "Demo", ProfileVersion: 1, Status: profile.StatusDraft,
	}
	env := setupEntryTestEnv(t, home, prof)
	binDir := filepath.Join(home, "bin")

	// Stub systemd-run that verifies --setenv=PYTHONUNBUFFERED=1 and StandardOutput/Error=append:...
	systemdRunScript := fmt.Sprintf(`#!/bin/sh
logpath=""
errpath=""
pyunbuf=""
for arg in "$@"; do
  case "$arg" in
    --property=StandardOutput=append:*)
      logpath="${arg#--property=StandardOutput=append:}"
      ;;
    --property=StandardError=append:*)
      errpath="${arg#--property=StandardError=append:}"
      ;;
    --setenv=PYTHONUNBUFFERED=1)
      pyunbuf="1"
      ;;
  esac
done
if [ -z "$logpath" ] || [ -z "$errpath" ] || [ "$pyunbuf" != "1" ]; then
  echo "missing append log or PYTHONUNBUFFERED=1: out=$logpath err=$errpath py=$pyunbuf" >&2
  exit 1
fi
echo "Ready at http://127.0.0.1:%d/app#token=sd-tok-999" >> "$logpath"
printf '  sl  local_address rem_address   st\n   0: 0100007F:%04X 00000000:0000 0A 00000000:00000000 00000000 00000000  1000        0 1\n' > %q
exit 0
`, port, port, filepath.Join(procDir, "tcp"))
	if err := os.WriteFile(filepath.Join(binDir, "systemd-run"), []byte(systemdRunScript), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "systemctl"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "demo"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	entry := profile.Entry{
		ID: "webapp", Kind: "web", Host: "127.0.0.1", Port: port, Path: "/",
		Start: &profile.EntryStart{
			Argv:        profile.Argv{"demo", "serve"},
			WaitSeconds: 5,
			CaptureURL:  true,
		},
	}

	res, err := StartEntry(context.Background(), env, home, login.EntryStartParams{InstanceID: "demo", Entry: entry})
	if err != nil {
		t.Fatalf("StartEntry(systemd-run): %v", err)
	}
	if res.ManagedUnit == "" || res.LogPath == "" {
		t.Fatalf("expected ManagedUnit and LogPath on systemd-run path, got %+v", res)
	}
	if res.AuthSuffix != "/app#token=sd-tok-999" {
		t.Fatalf("AuthSuffix = %q, want %q", res.AuthSuffix, "/app#token=sd-tok-999")
	}
	logBytes, err := os.ReadFile(res.LogPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if strings.Contains(string(logBytes), "sd-tok-999") || strings.Contains(string(logBytes), "http://") {
		t.Fatalf("systemd-run log file not sanitized: %q", string(logBytes))
	}

	// Simulate stray secret written before StopEntry and verify StopEntry re-sanitizes.
	if err := os.WriteFile(res.LogPath, []byte("ok line\nGET /?secret=leaked-on-stop HTTP/1.1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := StopEntry(context.Background(), env, home, login.EntryStopParams{InstanceID: "demo", Entry: entry}); err != nil {
		t.Fatalf("StopEntry: %v", err)
	}
	afterStop, err := os.ReadFile(res.LogPath)
	if err != nil {
		t.Fatalf("read log after stop: %v", err)
	}
	if strings.Contains(string(afterStop), "leaked-on-stop") || !strings.Contains(string(afterStop), "[REDACTED]") {
		t.Fatalf("StopEntry did not sanitize log: %q", string(afterStop))
	}
}

func TestStartEntryCaptureURLDelayedBannerAndPartialTail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	port, err := strconv.Atoi(srv.URL[strings.LastIndex(srv.URL, ":")+1:])
	if err != nil {
		t.Fatal(err)
	}

	home := t.TempDir()
	procDir := filepath.Join(home, "procnet")
	oldProc, oldPoll, oldPost := ProcNet, entryStartPoll, capturePostReadyWait
	ProcNet, entryStartPoll, capturePostReadyWait = procDir, 30*time.Millisecond, 600*time.Millisecond
	t.Cleanup(func() {
		ProcNet, entryStartPoll, capturePostReadyWait = oldProc, oldPoll, oldPost
	})
	writeFakeProcNet(t, procDir, 0)

	prof := profile.Profile{
		Schema: profile.Schema, ID: "demo", DisplayName: "Demo", ProfileVersion: 1, Status: profile.StatusDraft,
	}
	env := setupEntryTestEnv(t, home, prof)
	binDir := filepath.Join(home, "bin")

	// Listen immediately, then print ?key=... 120ms AFTER HTTP is already ready.
	demoScript := fmt.Sprintf(`#!/bin/sh
printf '  sl  local_address rem_address   st\n   0: 0100007F:%04X 00000000:0000 0A 00000000:00000000 00000000 00000000  1000        0 1\n' > %q
sleep 0.12
echo "Login path: /login?key=delayed-key-789"
sleep 30
`, port, filepath.Join(procDir, "tcp"))
	if err := os.WriteFile(filepath.Join(binDir, "demo"), []byte(demoScript), 0o700); err != nil {
		t.Fatal(err)
	}

	entry := profile.Entry{
		ID: "webapp", Kind: "web", Host: "127.0.0.1", Port: port, Path: "/",
		Start: &profile.EntryStart{
			Argv:        profile.Argv{"demo", "serve"},
			WaitSeconds: 5,
			CaptureURL:  true,
		},
	}
	res, err := StartEntry(context.Background(), env, home, login.EntryStartParams{InstanceID: "demo", Entry: entry})
	if err != nil {
		t.Fatalf("StartEntry delayed: %v", err)
	}
	t.Cleanup(func() {
		_, _ = StopEntry(context.Background(), env, home, login.EntryStopParams{InstanceID: "demo", Entry: entry})
	})
	if res.AuthSuffix != "/login?key=delayed-key-789" {
		t.Fatalf("expected delayed AuthSuffix, got %q", res.AuthSuffix)
	}

	// Verify sanitizeLogContent catches all param variants, authSuffix values, and incomplete tail lines.
	raw := "normal line\nauth=abc123\ncode=xyz999\nticket=tkt888\ncustom-val-secret-42\nen=HALF_LINE_SECRET"
	clean := sanitizeLogContent(raw, "/?foo=custom-val-secret-42")
	for _, leak := range []string{"abc123", "xyz999", "tkt888", "custom-val-secret-42", "HALF_LINE_SECRET"} {
		if strings.Contains(clean, leak) {
			t.Fatalf("sanitizeLogContent leaked %q in %q", leak, clean)
		}
	}
	if !strings.Contains(clean, "normal line") {
		t.Fatalf("normal line lost in %q", clean)
	}
	if suf := extractPortURLSuffix([]byte("\x1b[32mhttp://127.0.0.1:9120/#ticket=ansi-ticket-9\x1b[0m."), 9120); suf != "/#ticket=ansi-ticket-9" {
		t.Fatalf("extractPortURLSuffix ANSI = %q", suf)
	}
}

func TestPrepareAndStartRejectDossierLocalEntry(t *testing.T) {
	home := t.TempDir()
	procDir := filepath.Join(home, "procnet")
	oldProc := ProcNet
	ProcNet = procDir
	t.Cleanup(func() { ProcNet = oldProc })
	writeFakeProcNet(t, procDir, 0)

	prof := profile.Profile{
		Schema:         profile.Schema,
		ID:             "demo",
		DisplayName:    "Demo",
		ProfileVersion: 1,
		Status:         profile.StatusDraft,
	}
	env := setupEntryTestEnv(t, home, prof)

	rec, err := porterenroll.ReadRecord(home, "demo")
	if err != nil || rec == nil {
		t.Fatalf("ReadRecord: %+v %v", rec, err)
	}
	rec.LocalEntries = []enroll.LocalEntry{{
		ID:          "agent-ui",
		DisplayName: "Agent UI",
		Port:        7860,
		Path:        "/ui",
		Hint:        "Start UI manually",
	}}
	if _, err := porterenroll.UpdateRecord(home, *rec); err != nil {
		t.Fatal(err)
	}

	effProf, err := porterenroll.ReadProfile(home, "demo")
	if err != nil || len(effProf.Entries) != 1 || effProf.Entries[0].ID != "agent-ui" {
		t.Fatalf("ReadProfile entries = %+v err=%v", effProf, err)
	}

	ctx := context.Background()
	probe, err := ProbeEntry(ctx, env, home, login.EntryProbeParams{InstanceID: "demo", Entry: effProf.Entries[0]})
	if err != nil {
		t.Fatalf("ProbeEntry local entry: %v", err)
	}
	if probe.LocalSource != "agent_dossier" || probe.AgentHint != "Start UI manually" || probe.HasPrepare || probe.HasStart {
		t.Fatalf("unexpected probe: %+v", probe)
	}

	forged := effProf.Entries[0]
	forged.Prepare = &profile.EntryPrepare{Steps: []profile.Argv{{"sh", "-c", "evil"}}}
	forged.Start = &profile.EntryStart{Argv: profile.Argv{"demo", "serve"}}

	if _, err := PrepareEntry(ctx, env, home, login.EntryPrepareParams{InstanceID: "demo", Entry: forged}); err == nil {
		t.Fatal("PrepareEntry must reject dossier LocalEntry even with caller Prepare argv")
	} else if ue, ok := valise.As(err); !ok || ue.Code != "entry_not_declared" {
		t.Fatalf("unexpected PrepareEntry error: %v", err)
	}
	if _, err := StartEntry(ctx, env, home, login.EntryStartParams{InstanceID: "demo", Entry: forged}); err == nil {
		t.Fatal("StartEntry must reject dossier LocalEntry even with caller Start argv")
	} else if ue, ok := valise.As(err); !ok || ue.Code != "entry_not_declared" {
		t.Fatalf("unexpected StartEntry error: %v", err)
	}
}
