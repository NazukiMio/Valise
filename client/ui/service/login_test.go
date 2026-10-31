package service

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"valise/core/valise"
)

// The fake harness logs in by calling $BROWSER (device) or by printing a
// loopback OAuth link and waiting for its callback (oauth).
const demoLogin = `#!/bin/sh
case "$1" in
device) $BROWSER "https://device.example.test/activate"; echo "enter code ABCD-1234"; sleep 2; exit 3 ;;
oauth) echo "Open https://auth.example.test/authorize?redirect_uri=http%3A%2F%2Flocalhost%3A1%2Fcb&state=s"; sleep 30 ;;
esac
`

const demoLogins = `login:
  - { id: device, display_name: Device, kind: device_code, argv: [demo-login, device], timeout_seconds: 20 }
  - { id: oauth, display_name: OAuth, kind: loopback_oauth, argv: [demo-login, oauth], timeout_seconds: 20 }
`

func loginWorld(t *testing.T) (*Service, string) {
	t.Helper()
	s, home := enrollWorld(t)
	if _, err := s.Enroll(context.Background(), "box", EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(home, ".local/bin/demo-login"), []byte(demoLogin), 0o755)
	p := filepath.Join(s.Dirs.Data, "profiles", "demo.yaml")
	os.WriteFile(p, []byte(demoProfile+demoLogins), 0o644)
	return s, home
}

type statusLog struct {
	mu     sync.Mutex
	states []LoginStatus
}

func (l *statusLog) add(s LoginStatus) { l.mu.Lock(); l.states = append(l.states, s); l.mu.Unlock() }
func (l *statusLog) names() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var n []string
	for _, s := range l.states {
		n = append(n, s.State)
	}
	return strings.Join(n, ",")
}

func TestDeviceLoginThroughShim(t *testing.T) {
	s, home := loginWorld(t)
	ctx := context.Background()
	target, err := s.LoginMethods(ctx, "box", "demo")
	if err != nil || len(target.Profile.Login) != 2 {
		t.Fatalf("%+v %v", target, err)
	}
	var out bytes.Buffer
	var log statusLog
	var opened []string
	var confirmedDomain string
	st, err := s.RunLogin(ctx, "box", "demo", "device", LoginIO{
		Stdin: strings.NewReader(""), Stdout: &out, Status: log.add,
		ConfirmOpen: func(link, domain string) bool { confirmedDomain = domain; return true },
		OpenBrowser: func(link string) error { opened = append(opened, link); return nil },
	})
	if err != nil || st.ExitCode != 3 {
		t.Fatalf("%+v %v\n%s", st, err, out.String())
	}
	if !strings.Contains(out.String(), "ABCD-1234") {
		t.Fatalf("PTY output not relayed: %q", out.String())
	}
	// The command may exit before the link is relayed; wait for it.
	for i := 0; i < 40 && len(opened) == 0; i++ {
		time.Sleep(50 * time.Millisecond)
	}
	if len(opened) != 1 || opened[0] != "https://device.example.test/activate" || confirmedDomain != "device.example.test" {
		t.Fatalf("opened %v (domain %q); states %s", opened, confirmedDomain, log.names())
	}
	if names := log.names(); !strings.HasPrefix(names, "waiting_link") || !strings.Contains(names, "exited") {
		t.Fatalf("states %s", names)
	}
	sessions, _ := filepath.Glob(filepath.Join(home, ".local/state/valise/login/*"))
	if len(sessions) != 0 {
		t.Fatalf("session files left: %v", sessions)
	}
	audit, _ := os.ReadFile(filepath.Join(home, ".local/state/valise/audit.jsonl"))
	if !strings.Contains(string(audit), `"op":"login"`) || !strings.Contains(string(audit), `"result":"exit 3"`) ||
		strings.Contains(string(audit), "device.example.test") {
		t.Fatalf("audit: %s", audit)
	}
}

func TestLoopbackLoginWithoutCallbackListener(t *testing.T) {
	s, _ := loginWorld(t)
	old := callbackWait
	callbackWait = time.Second
	t.Cleanup(func() { callbackWait = old })
	var out bytes.Buffer
	var log statusLog
	opened := false
	_, err := s.RunLogin(context.Background(), "box", "demo", "oauth", LoginIO{
		Stdin: strings.NewReader(""), Stdout: &out, Status: log.add,
		OpenBrowser: func(string) error { opened = true; return nil },
	})
	ue, _ := valise.As(err)
	if ue == nil || ue.Code != "login_callback_missing" || opened {
		t.Fatalf("%v opened=%v states=%s\n%s", err, opened, log.names(), out.String())
	}
	if !strings.HasSuffix(log.names(), "exited") {
		t.Fatalf("login command not ended: %s", log.names())
	}
}

func TestUnknownLoginMethod(t *testing.T) {
	s, _ := loginWorld(t)
	_, err := s.RunLogin(context.Background(), "box", "demo", "nope", LoginIO{})
	if ue, _ := valise.As(err); ue == nil || ue.Code != "login_method_unknown" {
		t.Fatal(err)
	}
}
