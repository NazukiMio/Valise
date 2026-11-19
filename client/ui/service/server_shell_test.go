package service

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"valise/core/valise"
)

// shellWrap replaces the fake ssh: a forced-tty ("-tt") session without a
// remote command is a fixture shell (logs its argv, echoes stdin, exits with
// the code in <dir>/code, or sleeps when <dir>/hang exists); everything
// else goes to the real fake ssh.
func shellWrap(t *testing.T, s *Service) (dir string) {
	t.Helper()
	dir = t.TempDir()
	body := "#!/bin/sh\ncase \" $* \" in\n *\" -tt \"*)\n  echo \"$*\" > " + dir + "/argv\n  env > " + dir + "/env\n" +
		"  if [ -f " + dir + "/hang ]; then sleep 60 & echo $! > " + dir + "/child; wait; exit 0; fi\n" +
		"  cat\n  exit $(cat " + dir + "/code)\n  ;;\nesac\nexec " + s.Config.SSHBinary + " \"$@\"\n"
	wrap := filepath.Join(dir, "ssh-wrap.sh")
	if err := os.WriteFile(wrap, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "code"), []byte("0"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.Config.SSHBinary = wrap
	return dir
}

func TestServerShellGate(t *testing.T) {
	// An agent without an offsite backup blocks the shell.
	s, _, _, _ := sealedWorld(t)
	s.OwnerAuth.Passphrase = func(string) (string, error) { return pass, nil }
	dir := shellWrap(t, s)
	ctx := context.Background()
	var out bytes.Buffer
	code, err := s.RunServerShell(ctx, "box", NativeIO{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &out})
	if ue, ok := valise.As(err); !ok || ue.Code != GateCode || code != -1 {
		t.Fatalf("gate did not block: %d %v", code, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "argv")); err == nil {
		t.Fatal("a shell started behind the gate")
	}
	// Accepting the risk lets it through.
	code, err = s.RunServerShell(WithRiskAccepted(ctx), "box", NativeIO{Stdin: strings.NewReader("hi\n"), Stdout: &out, Stderr: &out})
	if err != nil || code != 0 || !strings.Contains(out.String(), "hi") {
		t.Fatalf("%d %v %q", code, err, out.String())
	}
	// A server with no agents passes directly.
	s2, _ := enrollWorld(t)
	shellWrap(t, s2)
	out.Reset()
	code, err = s2.RunServerShell(ctx, "box", NativeIO{Stdin: strings.NewReader("direct\n"), Stdout: &out, Stderr: &out})
	if err != nil || code != 0 || !strings.Contains(out.String(), "direct") {
		t.Fatalf("%d %v %q", code, err, out.String())
	}
}

func TestServerShellStreamsAndExitCodes(t *testing.T) {
	s, _ := enrollWorld(t)
	dir := shellWrap(t, s)
	ctx := context.Background()
	var out, errb bytes.Buffer
	code, err := s.RunServerShell(ctx, "box", NativeIO{Stdin: strings.NewReader("typed-by-user\n"), Stdout: &out, Stderr: &errb})
	if err != nil || code != 0 || !strings.Contains(out.String(), "typed-by-user") {
		t.Fatalf("%d %v %q %q", code, err, out.String(), errb.String())
	}
	argv, _ := os.ReadFile(filepath.Join(dir, "argv"))
	if !strings.Contains(" "+string(argv)+" ", " -tt ") || strings.Contains(" "+string(argv)+" ", " -t ") {
		t.Fatalf("not a forced tty: %q", argv)
	}
	os.WriteFile(filepath.Join(dir, "code"), []byte("7"), 0o644)
	out.Reset()
	code, err = s.RunServerShell(ctx, "box", NativeIO{Stdin: strings.NewReader("x\n"), Stdout: &out, Stderr: &errb})
	if err != nil || code != 7 {
		t.Fatalf("a non-zero exit is a result, not an error: %d %v", code, err)
	}
	os.WriteFile(filepath.Join(dir, "code"), []byte("255"), 0o644)
	code, err = s.RunServerShell(ctx, "box", NativeIO{Stdin: strings.NewReader("x\n"), Stdout: &out, Stderr: &errb})
	if ue, ok := valise.As(err); !ok || ue.Code != "terminal_conn_failed" || code != 255 {
		t.Fatalf("%d %v", code, err)
	}
}

func TestServerShellCancelKillsProcessGroup(t *testing.T) {
	s, _ := enrollWorld(t)
	dir := shellWrap(t, s)
	os.WriteFile(filepath.Join(dir, "hang"), nil, 0o644)
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type res struct {
		code int
		err  error
	}
	ch := make(chan res, 1)
	pr, pw, _ := os.Pipe() // a stdin that never closes
	defer pr.Close()
	defer pw.Close()
	go func() {
		c, err := s.RunServerShell(runCtx, "box", NativeIO{Stdin: pr, Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}})
		ch <- res{c, err}
	}()
	var child int
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline) && child == 0; time.Sleep(20 * time.Millisecond) {
		if b, err := os.ReadFile(filepath.Join(dir, "child")); err == nil {
			child, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		}
	}
	if child == 0 {
		t.Fatal("the shell never started")
	}
	at := time.Now()
	cancel()
	select {
	case r := <-ch:
		if r.code != -1 || r.err == nil || time.Since(at) > 2*time.Second {
			t.Fatalf("%+v after %v", r, time.Since(at))
		}
	case <-time.After(3 * time.Second):
		syscall.Kill(child, syscall.SIGKILL)
		t.Fatal("RunServerShell hung after cancel")
	}
	gone := false
	for i := 0; i < 20 && !gone; i++ {
		gone = syscall.Kill(child, 0) != nil
		time.Sleep(25 * time.Millisecond)
	}
	if !gone {
		syscall.Kill(child, syscall.SIGKILL)
		t.Fatal("the shell's child survived the cancel")
	}
}

func TestServerShellPropagatesAskpassEnv(t *testing.T) {
	s, _ := enrollWorld(t)
	dir := shellWrap(t, s)
	s.NoTerminalLogin = true
	s.PasswordAuth.Prompt = func(string) (string, error) { return "pw", nil }
	var out bytes.Buffer
	code, err := s.RunServerShell(context.Background(), "box", NativeIO{
		Stdin:  strings.NewReader(""),
		Stdout: &out,
		Stderr: &out,
	})
	if err != nil || code != 0 {
		t.Fatalf("%d %v", code, err)
	}
	envBytes, err := os.ReadFile(filepath.Join(dir, "env"))
	if err != nil {
		t.Fatal(err)
	}
	envStr := string(envBytes)
	for _, want := range []string{"SSH_ASKPASS=", "SSH_ASKPASS_REQUIRE=force"} {
		if !strings.Contains(envStr, want) {
			t.Fatalf("missing %q in spawned ssh env:\n%s", want, envStr)
		}
	}
}
