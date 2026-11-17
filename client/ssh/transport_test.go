package ssh

import (
	"context"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"valise/core/valise"
	"valise/testbed/fakesshenv"
)

func TestArgs(t *testing.T) {
	short := t.TempDir()
	if len(short) > maxControlDirLen {
		short = privateTmpDir()
	}
	tr, _ := New("box", Options{ControlDir: short})
	want := []string{"-T", "-o", "ConnectTimeout=15", "-o", "ServerAliveInterval=10", "-o", "ServerAliveCountMax=3", "-o", "ControlMaster=auto", "-o", "ControlPath=" + short + "/cm-%C", "-o", "ControlPersist=60", "--", "box", "uname"}
	if got := tr.Args("uname"); !reflect.DeepEqual(got, want) {
		t.Fatalf("%q", got)
	}
	tr, _ = New("box", Options{ControlDir: short, HostConfig: &HostConfig{ControlPath: "~/.ssh/cm-%C"}})
	if got := tr.Args("uname"); !reflect.DeepEqual(got, []string{"-T", "-o", "ConnectTimeout=15", "-o", "ServerAliveInterval=10", "-o", "ServerAliveCountMax=3", "--", "box", "uname"}) || !tr.ReusesUserControlMaster() {
		t.Fatalf("user ControlMaster must be reused: %q", got)
	}
	for _, bad := range []string{"", "-oProxyCommand=x", "a b"} {
		if _, err := New(bad, Options{}); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestLongControlDirFallsBack(t *testing.T) {
	long := filepath.Join(t.TempDir(), strings.Repeat("x", 80))
	tr, _ := New("box", Options{ControlDir: long})
	args := strings.Join(tr.Args("true"), " ")
	if strings.Contains(args, long) {
		t.Fatalf("socket path over the sun_path limit: %s", args)
	}
	if dir := privateTmpDir(); dir != "" && !strings.Contains(args, "ControlPath="+dir+"/cm-%C") {
		t.Fatalf("expected fallback to %s: %s", dir, args)
	}
}

func TestQuote(t *testing.T) {
	for in, want := range map[string]string{
		"/home/u/.local/bin/valise-porter": "/home/u/.local/bin/valise-porter",
		"it's":                             `'it'\''s'`,
		"$HOME/x":                          `'$HOME/x'`,
		"":                                 "''",
	} {
		if got := Quote(in); got != want {
			t.Errorf("Quote(%q) = %s", in, got)
		}
		out, err := exec.Command("sh", "-c", "printf %s "+Quote(in)).Output()
		if err != nil || string(out) != in {
			t.Errorf("sh round trip %q: %q %v", in, out, err)
		}
	}
}

func fakeTransport(t *testing.T, e *fakesshenv.Env, host string) *Transport {
	t.Helper()
	tr, err := New(host, Options{Binary: e.SSH, ControlDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

func TestRunOverFakeSSH(t *testing.T) {
	e := fakesshenv.New(t)
	home := e.AddHost("box")
	tr := fakeTransport(t, e, "box")
	res, err := tr.Run(context.Background(), `printf %s "$HOME"; cat; exit 3`, strings.NewReader("+in"))
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Stdout) != home+"+in" || res.ExitCode != 3 {
		t.Fatalf("%q %d", res.Stdout, res.ExitCode)
	}
}

func TestConnectFailure(t *testing.T) {
	e := fakesshenv.New(t)
	e.AddHost("box")
	t.Setenv("FAKESSH_FAIL_CONNECT", "box")
	_, err := fakeTransport(t, e, "box").Run(context.Background(), "true", nil)
	ue, ok := valise.As(err)
	if !ok || ue.Code != "ssh_connect_failed" || !strings.Contains(ue.Detail, "Connection refused") || ue.Suggestion == "" {
		t.Fatalf("%v", err)
	}
}

func TestMissingSSHBinary(t *testing.T) {
	tr, _ := New("box", Options{Binary: "/nonexistent/ssh"})
	_, err := tr.Run(context.Background(), "true", nil)
	if ue, ok := valise.As(err); !ok || ue.Code != "ssh_not_runnable" {
		t.Fatalf("%v", err)
	}
}

func TestTTYAndForwardArgs(t *testing.T) {
	tr, _ := New("box", Options{})
	if got := tr.TTYArgs("login"); !reflect.DeepEqual(got, []string{"-tt", "-o", "ConnectTimeout=15", "-o", "ServerAliveInterval=10", "-o", "ServerAliveCountMax=3", "--", "box", "login"}) {
		t.Fatalf("%v", got)
	}
	if got := tr.ForwardArgs("::1", 1455); !reflect.DeepEqual(got, []string{"-W", "[::1]:1455", "-o", "ConnectTimeout=15", "-o", "ServerAliveInterval=10", "-o", "ServerAliveCountMax=3", "--", "box"}) {
		t.Fatalf("%v", got)
	}
	if got := tr.ForwardArgs("127.0.0.1", 18789); !reflect.DeepEqual(got, []string{"-W", "127.0.0.1:18789", "-o", "ConnectTimeout=15", "-o", "ServerAliveInterval=10", "-o", "ServerAliveCountMax=3", "--", "box"}) {
		t.Fatalf("%v", got)
	}
}

// A daily-key master and a full login's master to the same host must not
// share a socket: OpenSSH keys masters by host, port and user only.
func TestControlNameSeparatesAccessLevels(t *testing.T) {
	dir := t.TempDir()
	full, _ := New("h", Options{ControlDir: dir})
	daily, _ := New("h", Options{ControlDir: dir, ControlName: "cmd-", Identity: "/k/daily"})
	path := func(tr *Transport) string {
		for _, a := range tr.Args("x") {
			if strings.HasPrefix(a, "ControlPath=") {
				return a
			}
		}
		return ""
	}
	if p1, p2 := path(full), path(daily); p1 == "" || p2 == "" || p1 == p2 || !strings.Contains(p2, "/cmd-%C") {
		t.Fatalf("%q %q", p1, p2)
	}
}

func TestModeInferenceAndProcessHook(t *testing.T) {
	dailyTr, _ := New("h", Options{ConfigFile: "/c", ControlName: "cmd-", BatchMode: true, Identity: "/k/daily"})
	adminTr, _ := New("h", Options{ConfigFile: "/c", BatchMode: true, Identity: "/k/admin"})
	userTr, _ := New("h", Options{HostConfig: &HostConfig{}})
	bootTr, _ := New("h", Options{ConfigFile: "/c", NoPubkey: true, ControlPath: "/tmp/op-1"})
	explicitTr, _ := New("h", Options{Mode: ModeAdmin, ConfigFile: "/c"})
	if dailyTr.Mode() != ModeDaily || adminTr.Mode() != ModeAdmin || userTr.Mode() != ModeUser || bootTr.Mode() != ModeBootstrap || explicitTr.Mode() != ModeAdmin {
		t.Fatalf("modes: daily=%q admin=%q user=%q boot=%q explicit=%q", dailyTr.Mode(), adminTr.Mode(), userTr.Mode(), bootTr.Mode(), explicitTr.Mode())
	}

	e := fakesshenv.New(t)
	e.AddHost("box")
	var mu sync.Mutex
	var events []ProcessEvent
	tr, err := New("box", Options{
		Binary:     e.SSH,
		ControlDir: t.TempDir(),
		Mode:       ModeDaily,
		ProcessHook: func(ev ProcessEvent) {
			mu.Lock()
			events = append(events, ev)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := tr.Run(context.Background(), "exit 7", nil)
	if err != nil || res.ExitCode != 7 {
		t.Fatalf("unexpected run result: res=%+v err=%v", res, err)
	}
	mu.Lock()
	got := append([]ProcessEvent{}, events...)
	mu.Unlock()
	if len(got) != 2 || got[0].Phase != "start" || got[0].Host != "box" || got[0].Mode != ModeDaily || got[0].Kind != "run" {
		t.Fatalf("unexpected start event: %+v", got)
	}
	if got[1].Phase != "end" || got[1].Host != "box" || got[1].Mode != ModeDaily || got[1].ExitCode != 7 || got[1].ErrCode != "nonzero_exit" {
		t.Fatalf("unexpected end event: %+v", got)
	}
}
