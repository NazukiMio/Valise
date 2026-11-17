package service

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"valise/client/config"
	"valise/core/install"
	"valise/core/rpc"
	"valise/core/vaultrpc"
	"valise/testbed/fakesshenv"
)

func TestInstanceNativeUsesAcceptedCommandOverFreshManagementFakeSSH(t *testing.T) {
	s, home := enrollWorld(t)
	p := filepath.Join(s.Dirs.Data, "profiles", "demo.yaml")
	if err := os.WriteFile(p, []byte(demoProfile+"native_cli: [/bin/cat]\nnative_hints: [accepted-instance-hint]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := WithRiskAccepted(context.Background())
	if _, err := s.Enroll(ctx, "box", EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatal(err)
	}
	info, err := s.InstanceNativeInfo(ctx, "box", "demo")
	if err != nil || len(info.Argv) != 1 || info.Argv[0] != "/bin/cat" || len(info.Hints) != 1 {
		t.Fatal(info, err)
	}
	// Local profile changes cannot substitute a different server command.
	if err := os.WriteFile(p, []byte(demoProfile+"native_cli: [/bin/echo, wrong-local-command]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd, done, err := s.InstanceNativeCommand(ctx, "box", "demo", NativeCLI)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Join(cmd.Args, " ")
	done()
	if !strings.Contains(line, "native --instance") || strings.Contains(line, "wrong-local-command") || strings.Contains(line, " exec ") || strings.Contains(line, "--shell") {
		t.Fatal("client supplied a native argv instead of the instance", line)
	}
	// This verifies fake-ssh streams and command scope, not a real PTY.
	before := readFile(t, filepath.Join(home, ".config/valise/dossiers/demo.json"))
	var output, stderr bytes.Buffer
	code, err := s.RunInstanceNative(ctx, "box", "demo", NativeCLI, NativeIO{Stdin: strings.NewReader("fixture-terminal-input"), Stdout: &output, Stderr: &stderr})
	if err != nil || code != 0 || output.String() != "fixture-terminal-input" {
		t.Fatal(code, err, output.String(), stderr.String())
	}
	if readFile(t, filepath.Join(home, ".config/valise/dossiers/demo.json")) != before {
		t.Fatal("native command rewrote enrollment")
	}
	for _, name := range []string{".local/state/valise/audit.jsonl", ".local/state/valise/sessions"} {
		path := filepath.Join(home, name)
		if st, err := os.Stat(path); err == nil && st.Mode().IsRegular() {
			b, _ := os.ReadFile(path)
			if bytes.Contains(b, []byte("fixture-terminal-input")) {
				t.Fatal("terminal contents recorded")
			}
		}
	}
}

func TestInstanceNativeInfoRequiresExplicitCapabilityAndIsReadOnlyTier(t *testing.T) {
	if !rpc.Classified(install.MethodNativeInfo) || rpc.TierOf(install.MethodNativeInfo) != rpc.TierDaily {
		t.Fatal("native metadata permissions")
	}
	if n2Compatibility(&rpc.HelloResult{}, install.MethodNativeInfo, nil) == nil {
		t.Fatal("old porter accepted typed native metadata")
	}
	if err := n2Compatibility(&rpc.HelloResult{Capabilities: []string{install.NativeCapability}}, install.MethodNativeInfo, nil); err != nil {
		t.Fatal(err)
	}
}

// The instance's interactive shell is the same fixed entry with one fixed
// switch: the porter, the instance, --shell, and no command of the
// caller's. Over fake-ssh the shell runs with the instance's environment:
// the profile's command directory is on its PATH.
func TestInstanceShellCarriesTheInstanceEnvironment(t *testing.T) {
	if got := nativeLine("/h/.local/bin/valise-porter", "demo", NativeShell); got != "/h/.local/bin/valise-porter native --instance demo --shell" {
		t.Fatalf("shell line: %q", got)
	}
	if got := nativeLine("/h/.local/bin/valise-porter", "demo", NativeCLI); got != "/h/.local/bin/valise-porter native --instance demo" {
		t.Fatalf("native line: %q", got)
	}
	// An instance id is quoted, never spliced as words of a command.
	if got := nativeLine("porter", "a; rm -rf x", NativeShell); !strings.HasPrefix(got, "porter native --instance 'a; rm -rf x'") {
		t.Fatalf("unquoted instance: %q", got)
	}

	s, home := enrollWorld(t)
	p := filepath.Join(s.Dirs.Data, "profiles", "demo.yaml")
	prof := strings.Replace(demoProfile, "detect: { binaries: [demo],", `detect: { bin_dirs: ["${HOME}/.demo-tools"], binaries: [demo],`, 1) +
		"native_cli: [/bin/cat]\n"
	if err := os.WriteFile(p, []byte(prof), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := WithRiskAccepted(context.Background())
	if _, err := s.Enroll(ctx, "box", EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatal(err)
	}
	info, err := s.InstanceNativeInfo(ctx, "box", "demo")
	if err != nil || !info.Shell {
		t.Fatalf("this porter must announce the shell: %+v %v", info, err)
	}
	if _, _, err := s.InstanceNativeCommand(ctx, "box", "demo", NativeMode("anything else")); err == nil {
		t.Fatal("an unknown mode was accepted")
	}
	// The shell is the user's own ($SHELL on the server), started as an
	// interactive login shell. A real /bin/sh is used: its login profile
	// runs (on macOS /etc/profile resets PATH), and the instance's command
	// directory must still be on PATH afterwards.
	t.Setenv("SHELL", "/bin/sh")
	var output, stderr bytes.Buffer
	code, err := s.RunInstanceNative(ctx, "box", "demo", NativeShell, NativeIO{
		Stdin: strings.NewReader("echo \"path=$PATH\"\ncase $- in *i*) echo interactive=yes;; esac\necho typed-by-user\nexit 0\n"), Stdout: &output, Stderr: &stderr})
	out := output.String()
	if err != nil || code != 0 {
		t.Fatal(code, err, out, stderr.String())
	}
	tools := filepath.Join(home, ".demo-tools")
	for _, want := range []string{"path=" + tools + ":", "interactive=yes", "typed-by-user"} {
		if !strings.Contains(out, want) {
			t.Errorf("the shell lacks %q:\n%s\n%s", want, out, stderr.String())
		}
	}
	if strings.Contains(out, "VALISE_INSTANCE_PATH") {
		t.Errorf("the hand-over variable leaked into the shell:\n%s", out)
	}
	// An old porter (no shell in its info) is not asked for one.
	if _, _, err := (&Service{}).InstanceNativeCommand(ctx, "box", "demo", NativeShell); err == nil {
		t.Fatal("no service state and still a command")
	}
}

func TestInstanceSessionRunsAcceptedSessionCLIAndAllowsSessionOnly(t *testing.T) {
	if got := nativeLine("/h/.local/bin/valise-porter", "demo", NativeSession); got != "/h/.local/bin/valise-porter native --instance demo --session" {
		t.Fatalf("session line: %q", got)
	}
	s, _ := enrollWorld(t)
	p := filepath.Join(s.Dirs.Data, "profiles", "demo.yaml")
	prof := demoProfile + "native_cli: [/bin/cat]\nnative_session_cli: [/bin/echo, session-ready]\n"
	if err := os.WriteFile(p, []byte(prof), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := WithRiskAccepted(context.Background())
	if _, err := s.Enroll(ctx, "box", EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatal(err)
	}
	info, err := s.InstanceNativeInfo(ctx, "box", "demo")
	if err != nil || !info.Session || !slices.Equal(info.SessionArgv, []string{"/bin/echo", "session-ready"}) {
		t.Fatalf("unexpected info: %+v %v", info, err)
	}
	// Changing local profile cannot hijack the server's accepted session command.
	if err := os.WriteFile(p, []byte(demoProfile+"native_session_cli: [/bin/echo, hijacked]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd, done, err := s.InstanceNativeCommand(ctx, "box", "demo", NativeSession)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Join(cmd.Args, " ")
	done()
	if !strings.Contains(line, "native --instance demo --session") || strings.Contains(line, "hijacked") {
		t.Fatalf("unexpected session cmd: %s", line)
	}
	var out, errb bytes.Buffer
	code, err := s.RunInstanceNative(ctx, "box", "demo", NativeSession, NativeIO{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb})
	if err != nil || code != 0 || strings.TrimSpace(out.String()) != "session-ready" {
		t.Fatalf("session run: code=%d err=%v out=%q err=%q", code, err, out.String(), errb.String())
	}

	// A profile declaring only native_session_cli still advertises native availability
	// and supports session + shell while refusing onboarding.
	s2, _ := enrollWorld(t)
	p2 := filepath.Join(s2.Dirs.Data, "profiles", "demo.yaml")
	if err := os.WriteFile(p2, []byte(demoProfile+"native_session_cli: [/bin/echo, only-session]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Enroll(ctx, "box", EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatal(err)
	}
	list, err := s2.Instances(ctx, "box")
	if err != nil || len(list) != 1 || !list[0].NativeCLI {
		t.Fatalf("session-only instance summary: %+v %v", list, err)
	}
	if _, _, err := s2.InstanceNativeCommand(ctx, "box", "demo", NativeCLI); err == nil {
		t.Fatal("onboard allowed without native_cli")
	}
	out.Reset()
	if code, err := s2.RunInstanceNative(ctx, "box", "demo", NativeSession, NativeIO{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb}); err != nil || code != 0 || strings.TrimSpace(out.String()) != "only-session" {
		t.Fatalf("session-only run: code=%d err=%v out=%q", code, err, out.String())
	}
}

func TestPauseAndRestoreInstanceServicesRequiresOwnerAndSeparatesHealth(t *testing.T) {
	e := fakesshenv.New(t)
	home := e.AddHost("vps")
	e.InstallPorter("vps")
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(home, ".demo/demo.json"), "{}")
	write(filepath.Join(home, ".local/bin/demo"), "#!/bin/sh\necho demo 1.0\n")
	write(filepath.Join(home, ".local/bin/loginctl"), "#!/bin/sh\necho yes\n")
	write(filepath.Join(home, ".local/bin/systemctl"), `#!/bin/sh
f="$HOME/unit-state"
[ -f "$f" ] || echo active > "$f"
shift # --user
case "$1" in
show)
  st=$(cat "$f")
  printf 'LoadState=loaded\nActiveState=%s\nUnitFileState=enabled\n' "$st"
  ;;
is-active) cat "$f" ;;
stop) echo inactive > "$f" ;;
reset-failed) ;;
start)
  if [ -f "$HOME/unit-broken" ]; then
    echo failed > "$f"
  else
    echo active > "$f"
  fi
  ;;
esac
`)
	s := newService(t, e.SSH)
	dir := s.Dirs.Data
	write(filepath.Join(home, "unit-state"), "active\n")
	// Configure vps as a tiered server (Valise daily key) so owner proof is enforced.
	if err := config.SaveServerList(dir, []config.Server{{Name: "vps", Source: config.SourceValise, HostName: "127.0.0.1", Port: 22, User: "me"}}); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(config.KeyDir(dir, "vps"), "daily")
	write(keyPath, "fake-private-key")
	write(keyPath+".pub", "ssh-ed25519 AAAATEST daily")
	write(filepath.Join(home, ".ssh/authorized_keys"), `restrict,pty,command="$HOME/.local/bin/valise-porter serve --tier daily" ssh-ed25519 AAAATEST daily`+"\n")

	prof := demoProfile + `service:
  candidates: [ { scope: user, unit: demo.service } ]
health:
  checks:
    - { kind: systemd_active }
native_cli: [/bin/echo, onboard-ran]
native_session_cli: [/bin/echo, session-ran]
`
	write(filepath.Join(dir, "profiles/demo.yaml"), prof)
	const pass = "test-passphrase-1234"
	if _, err := s.KeyInit(pass); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, _, err := s.KeyRegister(ctx, "vps", "laptop"); err != nil {
		t.Fatal(err)
	}
	s.OwnerAuth.Passphrase = func(string) (string, error) { return pass, nil }
	if _, err := s.Enroll(ctx, "vps", EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatal(err)
	}

	// Without offsite backup or risk acceptance, PauseInstanceServices must refuse BEFORE stopping service.
	_, err := s.PauseInstanceServices(ctx, "vps", "demo")
	wantCode(t, err, GateCode)
	if got := strings.TrimSpace(readFile(t, filepath.Join(home, "unit-state"))); got != "active" {
		t.Fatalf("gate did not protect service: %q", got)
	}

	// Without owner proof, pausing must fail with owner_locked.
	s.ForgetOwner()
	origAuth := s.OwnerAuth
	s.OwnerAuth = OwnerAuth{}
	_, err = s.PauseInstanceServices(WithRiskAccepted(ctx), "vps", "demo")
	wantCode(t, err, "owner_locked")
	s.OwnerAuth = origAuth

	// With owner proof, pausing stops demo.service.
	stopped, err := s.PauseInstanceServices(WithRiskAccepted(ctx), "vps", "demo")
	if err != nil || !slices.Equal(stopped, []string{"demo.service"}) {
		t.Fatalf("pause: stopped=%v err=%v", stopped, err)
	}
	if got := strings.TrimSpace(readFile(t, filepath.Join(home, "unit-state"))); got != "inactive" {
		t.Fatalf("service not stopped: %q", got)
	}

	// Restoring when unit comes back active verifies resolved.
	res, err := s.RestoreInstanceServices(ctx, "vps", "demo", stopped)
	if err != nil || !slices.Equal(res.StartedUnits, []string{"demo.service"}) || !res.Checked ||
		len(res.Service) != 1 || res.Service[0].Active != "active" ||
		res.Verification == nil || res.Verification.State != "resolved" {
		t.Fatalf("restore healthy: %+v err=%v", res, err)
	}

	// When unit starts then goes failed, StartedUnits still records the start call
	// while Verification honestly reports remaining (never falsely green).
	origSettle := nativeServiceSettle
	nativeServiceSettle = 30 * time.Millisecond
	t.Cleanup(func() { nativeServiceSettle = origSettle })
	write(filepath.Join(home, "unit-broken"), "1")
	resBad, err := s.RestoreInstanceServices(ctx, "vps", "demo", stopped)
	if err != nil || !slices.Equal(resBad.StartedUnits, []string{"demo.service"}) ||
		resBad.Verification == nil || resBad.Verification.State != "remaining" {
		t.Fatalf("restore broken: %+v err=%v", resBad, err)
	}
}

func TestNativeCommandSkipsHangingRemoteTargetGateAndCancelKillsProcessGroup(t *testing.T) {
	s, home, _, ss := sealedWorld(t)
	ctx := context.Background()
	s.OwnerAuth.Passphrase = func(string) (string, error) { return pass, nil }
	if err := os.WriteFile(filepath.Join(s.Dirs.Data, "profiles", "demo.yaml"), []byte(demoProfile+"native_cli: [/bin/echo, ok]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enroll(ctx, "box", EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.ServerNameSet(ctx, "box", "box"); err != nil {
		t.Fatal(err)
	}

	// Simulate an offline target whose listing hangs 30s; gate must skip within 3s.
	hanging := "#!/bin/sh\nsleep 30\n"
	for _, tool := range []string{"rsync", "ssh"} {
		if err := os.WriteFile(filepath.Join(home, ".local/bin", tool), []byte(hanging), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.TargetSet(ctx, "box", vaultrpc.Target{Name: "nas", Kind: "rsync", Dest: "nas:/backup"}); err != nil {
		t.Fatal(err)
	}
	if err := s.call(ctx, "box", vaultrpc.MethodSyncRecord, vaultrpc.SyncRecordParams{Target: "nas", Snapshots: []string{ss}}, nil); err != nil {
		t.Fatal(err)
	}

	origGate := targetGateTimeout
	targetGateTimeout = 200 * time.Millisecond
	defer func() { targetGateTimeout = origGate }()

	start := time.Now()
	_, _, err := s.InstanceNativeCommand(ctx, "box", "demo", NativeCLI)
	if elapsed := time.Since(start); elapsed >= 2*time.Second {
		t.Fatalf("InstanceNativeCommand blocked on hanging gate: %v", elapsed)
	}
	wantCode(t, err, GateCode)

	// Cancelling ctx kills local ssh process group immediately (even if ssh spawned local helpers)
	// and RunInstanceNative returns immediately without blocking on a 60s child.
	if err := os.WriteFile(filepath.Join(s.Dirs.Data, "profiles", "demo.yaml"), []byte(demoProfile+"native_cli: [/bin/sleep, \"60\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enroll(ctx, "box", EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatal(err)
	}
	localPIDFile := filepath.Join(home, "ssh-local-child.pid")
	sshWrap := filepath.Join(home, "ssh-wrap.sh")
	origSSH := s.Config.SSHBinary
	wrapBody := "#!/bin/sh\ncase \"$*\" in\n *\"native --instance\"*)\n  sleep 60 &\n  echo $! > " + localPIDFile + "\n  wait\n  exit 0\n  ;;\nesac\nexec " + origSSH + " \"$@\"\n"
	if err := os.WriteFile(sshWrap, []byte(wrapBody), 0o755); err != nil {
		t.Fatal(err)
	}
	s.Config.SSHBinary = sshWrap
	defer func() { s.Config.SSHBinary = origSSH }()

	runCtx, cancel := context.WithCancel(WithRiskAccepted(ctx))
	type runOut struct {
		code int
		err  error
	}
	outCh := make(chan runOut, 1)
	go func() {
		code, rerr := s.RunInstanceNative(runCtx, "box", "demo", NativeCLI, NativeIO{})
		outCh <- runOut{code: code, err: rerr}
	}()

	deadline := time.Now().Add(5 * time.Second)
	var childPID int
	for time.Now().Before(deadline) {
		if b, rerr := os.ReadFile(localPIDFile); rerr == nil && len(strings.TrimSpace(string(b))) > 0 {
			if n, perr := strconv.Atoi(strings.TrimSpace(string(b))); perr == nil && n > 0 {
				childPID = n
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if childPID == 0 {
		cancel()
		t.Fatal("ssh local child process did not start")
	}

	cancelAt := time.Now()
	cancel()
	select {
	case <-outCh:
		if time.Since(cancelAt) > 2*time.Second {
			t.Fatalf("RunInstanceNative cancel took too long: %v", time.Since(cancelAt))
		}
	case <-time.After(3 * time.Second):
		_ = syscall.Kill(childPID, syscall.SIGKILL)
		t.Fatal("RunInstanceNative hung after cancel")
	}
	gone := false
	for i := 0; i < 20; i++ {
		if kerr := syscall.Kill(childPID, 0); kerr != nil {
			gone = true
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !gone {
		_ = syscall.Kill(childPID, syscall.SIGKILL)
		t.Fatalf("descendant pid %d survived local ssh process group cancel", childPID)
	}

	// closeMasterAsync returns immediately even if CloseMaster blocks.
	t0 := time.Now()
	startedClose := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	closeMasterAsync(func() {
		close(startedClose)
		<-release
	})
	if time.Since(t0) > 100*time.Millisecond {
		t.Fatalf("closeMasterAsync blocked caller: %v", time.Since(t0))
	}
	<-startedClose
}
