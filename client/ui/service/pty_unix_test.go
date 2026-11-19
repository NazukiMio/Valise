//go:build darwin || linux

package service

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"valise/core/valise"
)

func ptyProbeWrap(t *testing.T, s *Service, wantSize string) (dir string) {
	t.Helper()
	dir = t.TempDir()
	body := `#!/bin/sh
case " $* " in
 *" -tt "*)
  echo "$*" > "` + dir + `/argv"
  if test -t 0 && test -t 1; then echo "TTY=ok"; else echo "TTY=no"; fi
  echo "TERM=$TERM"
  echo "COLORTERM=$COLORTERM"
  echo "SSH_ASKPASS_REQUIRE=$SSH_ASKPASS_REQUIRE"
  s0=$(stty size 2>/dev/null)
  echo "SIZE0=$s0"
  : > "` + dir + `/ready"
  i=0
  s1="$s0"
  while [ "$i" -lt 250 ]; do
    s1=$(stty size 2>/dev/null)
    if [ "$s1" = "` + wantSize + `" ]; then
      break
    fi
    i=$((i + 1))
    sleep 0.02
  done
  echo "SIZE1=$s1"
  IFS= read -r line
  printf 'GOT:%s\n' "$line"
  exit 0
  ;;
esac
exec "` + s.Config.SSHBinary + `" "$@"
`
	wrap := filepath.Join(dir, "ssh-pty-wrap.sh")
	if err := os.WriteFile(wrap, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	s.Config.SSHBinary = wrap
	return dir
}

func waitFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

func TestServerShellPTYModesEnvAndResize(t *testing.T) {
	s, _ := enrollWorld(t)
	s.NoTerminalLogin = true
	s.PasswordAuth.Prompt = func(string) (string, error) { return "pw", nil }
	dir := ptyProbeWrap(t, s, "40 132")

	var mu sync.Mutex
	var resizeFn func(int, int, int, int)
	inR, inW := io.Pipe()
	defer inW.Close()
	var out bytes.Buffer
	syncOut := &syncWriter{w: &out}

	type result struct {
		code int
		err  error
	}
	done := make(chan result, 1)
	go func() {
		c, err := s.RunServerShell(context.Background(), "box", NativeIO{
			Stdin:  inR,
			Stdout: syncOut,
			Stderr: syncOut,
			PTY:    true,
			OnResize: func(fn func(int, int, int, int)) {
				mu.Lock()
				resizeFn = fn
				mu.Unlock()
			},
		})
		done <- result{c, err}
	}()

	waitFile(t, filepath.Join(dir, "ready"))
	mu.Lock()
	fn := resizeFn
	mu.Unlock()
	if fn == nil {
		t.Fatal("OnResize callback was not registered")
	}
	fn(132, 40, 1056, 640)
	if _, err := io.WriteString(inW, "hello\r"); err != nil {
		t.Fatal(err)
	}

	res := <-done
	if res.err != nil || res.code != 0 {
		t.Fatalf("code=%d err=%v out=%q", res.code, res.err, out.String())
	}
	syncOut.mu.Lock()
	got := out.String()
	syncOut.mu.Unlock()
	for _, want := range []string{
		"TTY=ok",
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
		"SSH_ASKPASS_REQUIRE=force",
		"SIZE0=24 80",
		"SIZE1=40 132",
		"GOT:hello",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in PTY output:\n%s", want, got)
		}
	}
}

func TestInstanceNativePTYModesAndResize(t *testing.T) {
	s, _ := enrollWorld(t)
	prof := demoProfile + "native_cli: [/bin/echo, onboard]\n"
	if err := os.WriteFile(filepath.Join(s.Dirs.Data, "profiles", "demo.yaml"), []byte(prof), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := s.Enroll(ctx, "box", EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatal(err)
	}
	dir := ptyProbeWrap(t, s, "36 120")

	var mu sync.Mutex
	var resizeFn func(int, int, int, int)
	inR, inW := io.Pipe()
	defer inW.Close()
	var out bytes.Buffer
	syncOut := &syncWriter{w: &out}

	type result struct {
		code int
		err  error
	}
	done := make(chan result, 1)
	go func() {
		c, err := s.RunInstanceNative(WithRiskAccepted(ctx), "box", "demo", NativeCLI, NativeIO{
			Stdin:  inR,
			Stdout: syncOut,
			Stderr: syncOut,
			PTY:    true,
			OnResize: func(fn func(int, int, int, int)) {
				mu.Lock()
				resizeFn = fn
				mu.Unlock()
			},
		})
		done <- result{c, err}
	}()

	waitFile(t, filepath.Join(dir, "ready"))
	mu.Lock()
	fn := resizeFn
	mu.Unlock()
	if fn == nil {
		t.Fatal("OnResize callback was not registered")
	}
	fn(120, 36, 960, 576)
	if _, err := io.WriteString(inW, "native-ok\r"); err != nil {
		t.Fatal(err)
	}

	res := <-done
	if res.err != nil || res.code != 0 {
		t.Fatalf("code=%d err=%v out=%q", res.code, res.err, out.String())
	}
	syncOut.mu.Lock()
	got := out.String()
	syncOut.mu.Unlock()
	for _, want := range []string{"TTY=ok", "TERM=xterm-256color", "COLORTERM=truecolor", "SIZE1=36 120", "GOT:native-ok"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in native PTY output:\n%s", want, got)
		}
	}
}

func TestServerShellPTYCancelAndExitCodes(t *testing.T) {
	s, _ := enrollWorld(t)
	dir := shellWrap(t, s)
	ctx := context.Background()

	if err := os.WriteFile(filepath.Join(dir, "code"), []byte("7"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	code, err := s.RunServerShell(ctx, "box", NativeIO{
		Stdin:  strings.NewReader("\x04"),
		Stdout: &out,
		Stderr: &out,
		PTY:    true,
	})
	if err != nil || code != 7 {
		t.Fatalf("non-zero exit: code=%d err=%v", code, err)
	}

	if err := os.WriteFile(filepath.Join(dir, "code"), []byte("255"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, err = s.RunServerShell(ctx, "box", NativeIO{
		Stdin:  strings.NewReader("\x04"),
		Stdout: &out,
		Stderr: &out,
		PTY:    true,
	})
	if ue, ok := valise.As(err); !ok || ue.Code != "terminal_conn_failed" || code != 255 {
		t.Fatalf("conn failed: code=%d err=%v", code, err)
	}

	if err := os.WriteFile(filepath.Join(dir, "hang"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	type res struct {
		code int
		err  error
	}
	ch := make(chan res, 1)
	pr, pw, err := os.Pipe() // stdin that never closes
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	defer pw.Close()
	go func() {
		c, err := s.RunServerShell(runCtx, "box", NativeIO{
			Stdin:  pr,
			Stdout: &bytes.Buffer{},
			Stderr: &bytes.Buffer{},
			PTY:    true,
		})
		ch <- res{c, err}
	}()

	var child int
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline) && child == 0; time.Sleep(20 * time.Millisecond) {
		if b, err := os.ReadFile(filepath.Join(dir, "child")); err == nil {
			child, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		}
	}
	if child == 0 {
		t.Fatal("PTY shell never started")
	}
	at := time.Now()
	cancel()
	select {
	case r := <-ch:
		if r.code != -1 || r.err == nil || time.Since(at) > 2*time.Second {
			t.Fatalf("%+v after %v", r, time.Since(at))
		}
	case <-time.After(3 * time.Second):
		_ = syscall.Kill(child, syscall.SIGKILL)
		t.Fatal("RunServerShell PTY hung after cancel")
	}
	gone := false
	for i := 0; i < 20 && !gone; i++ {
		gone = syscall.Kill(child, 0) != nil
		time.Sleep(25 * time.Millisecond)
	}
	if !gone {
		_ = syscall.Kill(child, syscall.SIGKILL)
		t.Fatal("PTY shell child survived cancel")
	}
}
