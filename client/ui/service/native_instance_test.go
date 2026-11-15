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
	cmd, done, err := s.InstanceNativeCommand(ctx, "box", "demo")
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Join(cmd.Args, " ")
	done()
	if !strings.Contains(line, "native --instance") || strings.Contains(line, "wrong-local-command") || strings.Contains(line, " exec ") {
		t.Fatal("client supplied a native argv instead of the instance", line)
	}
	// This verifies fake-ssh streams and command scope, not a real PTY.
	before := readFile(t, filepath.Join(home, ".config/valise/dossiers/demo.json"))
	var output, stderr bytes.Buffer
	code, err := s.RunInstanceNative(ctx, "box", "demo", NativeIO{Stdin: strings.NewReader("fixture-terminal-input"), Stdout: &output, Stderr: &stderr})
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
