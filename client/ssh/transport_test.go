package ssh

import (
	"context"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
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
	want := []string{"-T", "-o", "ControlMaster=auto", "-o", "ControlPath=" + short + "/cm-%C", "-o", "ControlPersist=60", "--", "box", "uname"}
	if got := tr.Args("uname"); !reflect.DeepEqual(got, want) {
		t.Fatalf("%q", got)
	}
	tr, _ = New("box", Options{ControlDir: short, HostConfig: &HostConfig{ControlPath: "~/.ssh/cm-%C"}})
	if got := tr.Args("uname"); !reflect.DeepEqual(got, []string{"-T", "--", "box", "uname"}) || !tr.ReusesUserControlMaster() {
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
	if got := tr.TTYArgs("login"); !reflect.DeepEqual(got, []string{"-tt", "--", "box", "login"}) {
		t.Fatalf("%v", got)
	}
	if got := tr.ForwardArgs("::1", 1455); !reflect.DeepEqual(got, []string{"-W", "[::1]:1455", "--", "box"}) {
		t.Fatalf("%v", got)
	}
	if got := tr.ForwardArgs("127.0.0.1", 18789); !reflect.DeepEqual(got, []string{"-W", "127.0.0.1:18789", "--", "box"}) {
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
