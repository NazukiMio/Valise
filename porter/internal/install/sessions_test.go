package install

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestRunAcceptedTerminalForwardsHangupAndKillsStubbornChild(t *testing.T) {
	oldGrace := terminalHangupGrace
	terminalHangupGrace = 250 * time.Millisecond
	t.Cleanup(func() { terminalHangupGrace = oldGrace })

	home := t.TempDir()
	startedFile := filepath.Join(home, "started")
	// Child traps SIGHUP so it would linger forever unless escalated to SIGKILL.
	cmd := exec.Command("/bin/sh", "-c", `trap '' HUP; touch "$1"; sleep 30`, "sh", startedFile)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	type res struct {
		code int
		err  error
	}
	ch := make(chan res, 1)
	go func() {
		c, e := RunAcceptedTerminal(home, cmd, []string{"demo", "doctor"})
		ch <- res{c, e}
	}()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(startedFile); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := os.Stat(startedFile); err != nil {
		t.Fatal("child did not start in time")
	}

	if err := syscall.Kill(os.Getpid(), syscall.SIGHUP); err != nil {
		t.Fatalf("send SIGHUP: %v", err)
	}

	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("unexpected error: %v", r.err)
		}
		if r.code == 0 {
			t.Fatalf("expected non-zero exit code after SIGHUP termination, got 0")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("RunAcceptedTerminal did not terminate stubborn child after SIGHUP")
	}
}

func TestRunAcceptedTerminalForwardsSIGTERM(t *testing.T) {
	oldGrace := terminalHangupGrace
	terminalHangupGrace = 2 * time.Second
	t.Cleanup(func() { terminalHangupGrace = oldGrace })

	home := t.TempDir()
	startedFile := filepath.Join(home, "started")
	cmd := exec.Command("/bin/sh", "-c", `touch "$1"; sleep 30`, "sh", startedFile)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	ch := make(chan int, 1)
	go func() {
		code, _ := RunAcceptedTerminal(home, cmd, []string{"demo", "shell"})
		ch <- code
	}()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(startedFile); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("send SIGTERM: %v", err)
	}

	select {
	case code := <-ch:
		if code == 0 {
			t.Fatalf("expected non-zero exit code after SIGTERM, got 0")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("RunAcceptedTerminal did not terminate child after SIGTERM")
	}
}

func TestRunAcceptedTerminalKeepsSIGQUITSemantics(t *testing.T) {
	home := t.TempDir()
	startedFile := filepath.Join(home, "started")
	cmd := exec.Command("/bin/sh", "-c", `touch "$1"; sleep 0.3; exit 0`, "sh", startedFile)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	type res struct {
		code int
		err  error
	}
	ch := make(chan res, 1)
	go func() {
		c, e := RunAcceptedTerminal(home, cmd, []string{"demo", "chat"})
		ch <- res{c, e}
	}()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(startedFile); err == nil {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}

	// SIGQUIT should not force-kill the child; child exits cleanly with 0.
	if err := syscall.Kill(os.Getpid(), syscall.SIGQUIT); err != nil {
		t.Fatalf("send SIGQUIT: %v", err)
	}

	select {
	case r := <-ch:
		if r.err != nil || r.code != 0 {
			t.Fatalf("expected clean exit after SIGQUIT, got code=%d err=%v", r.code, r.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("RunAcceptedTerminal did not return")
	}
}
