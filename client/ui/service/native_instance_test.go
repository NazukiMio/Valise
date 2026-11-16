package service

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"valise/core/install"
	"valise/core/rpc"
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
