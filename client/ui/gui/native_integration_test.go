package gui

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/client/config"
	"valise/testbed/fakesshenv"
	"valise/ui/service"
)

const nativeTestProfile = `schema: valise-profile/1
id: demo
display_name: Demo
profile_version: 1
status: draft
detect: { binaries: [demo], state_dir_candidates: ["${HOME}/.demo"], markers_any: [demo.json] }
paths: [{ root: state, glob: "demo.json", category: config, verify: pending }]
provenance: { kind: binary, binary: { name: demo } }
service:
  candidates: [ { scope: user, unit: demo.service } ]
health:
  checks:
    - { kind: systemd_active }
native_cli: [/bin/sh, -c, "cat \"$HOME/unit-state\" > \"$HOME/seen-during-onboard\"; echo onboard-done"]
native_session_cli: [/bin/echo, session-done]
`

type nativeFlowWorld struct {
	a    *App
	tt   *ui.Tester
	mu   sync.Mutex
	home string
	pass string
}

func newNativeFlowWorld(t *testing.T) *nativeFlowWorld {
	t.Helper()
	e := fakesshenv.New(t)
	w := &nativeFlowWorld{home: e.AddHost("vps"), pass: "test-passphrase-1234"}
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
	write(filepath.Join(w.home, ".demo/demo.json"), "{}")
	write(filepath.Join(w.home, ".local/bin/demo"), "#!/bin/sh\necho demo 1.0\n")
	write(filepath.Join(w.home, ".local/bin/loginctl"), "#!/bin/sh\necho yes\n")
	write(filepath.Join(w.home, "unit-state"), "active\n")
	write(filepath.Join(w.home, ".local/bin/systemctl"), `#!/bin/sh
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
	dir := t.TempDir()
	s := &service.Service{Dirs: config.Dirs{Data: filepath.Join(dir, "data"), Cache: filepath.Join(dir, "cache")},
		Config: config.Config{SSHBinary: e.SSH}, Version: "test", Home: dir, SSHConfigPath: filepath.Join(dir, "ssh_config")}
	t.Cleanup(s.Close)
	if err := config.SaveServerList(s.Dirs.Data, []config.Server{{Name: "vps", Source: config.SourceValise, HostName: "127.0.0.1", Port: 22, User: "me"}}); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(config.KeyDir(s.Dirs.Data, "vps"), "daily")
	write(keyPath, "fake-private-key")
	write(keyPath+".pub", "ssh-ed25519 AAAATEST daily")
	write(filepath.Join(w.home, ".ssh/authorized_keys"), `restrict,pty,command="$HOME/.local/bin/valise-porter serve --tier daily" ssh-ed25519 AAAATEST daily`+"\n")
	write(filepath.Join(s.Dirs.Data, "profiles/demo.yaml"), nativeTestProfile)
	if _, err := s.KeyInit(w.pass); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, _, err := s.KeyRegister(ctx, "vps", "laptop"); err != nil {
		t.Fatal(err)
	}
	s.OwnerAuth.Passphrase = func(string) (string, error) { return w.pass, nil }
	if _, err := s.Enroll(ctx, "vps", service.EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatal(err)
	}
	s.ForgetOwner()

	w.a = New(s)
	s.NoTerminalLogin = true
	s.OwnerAuth.Passphrase = w.a.OwnerPassphrase
	s.PasswordAuth.Prompt = w.a.ServerPassword
	w.a.update = func(f func()) { w.mu.Lock(); defer w.mu.Unlock(); f() }
	w.a.scanTimeline = func(context.Context, service.TimelineScanRequest, func(service.TimelineUpdate)) error { return nil }
	w.a.newNativeTestTerminal = func(rw io.ReadWriteCloser) (io.Closer, error) {
		var buf bytes.Buffer
		go func() { _, _ = io.Copy(&buf, rw) }()
		return io.NopCloser(nil), nil
	}
	w.a.refreshAgents()
	w.tt = ui.NewTester(w.a.View, 1180, 900)
	w.wait(t, "agent list", func() bool { return len(w.a.agents.value.Items) == 1 })
	return w
}

func (w *nativeFlowWorld) do(f func()) {
	w.mu.Lock()
	defer w.mu.Unlock()
	f()
	w.tt.Frame()
	w.tt.Advance(time.Second)
}

func (w *nativeFlowWorld) wait(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		var ok bool
		w.do(func() { ok = cond() })
		if ok {
			return
		}
		if time.Now().After(deadline) {
			w.mu.Lock()
			defer w.mu.Unlock()
			t.Fatalf("timed out waiting for %s: %q", what, w.tt.Texts())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (w *nativeFlowWorld) click(t *testing.T, label string) {
	t.Helper()
	w.do(func() {
		if err := w.tt.Click(label); err != nil {
			t.Fatalf("%v in %q", err, w.tt.Texts())
		}
	})
}

func (w *nativeFlowWorld) answerOwner(t *testing.T, done func() bool) int {
	t.Helper()
	asked := 0
	w.wait(t, "owner passphrase or completion", func() bool {
		if len(w.a.asks) == 0 {
			return done()
		}
		w.tt.Frame()
		asked++
		w.tt.Type(w.pass)
		if err := w.tt.Click(T("common.ok")); err != nil {
			t.Fatal(err)
		}
		return false
	})
	return asked
}

func TestNativePauseServiceOnboardAndRestoreFlow(t *testing.T) {
	w := newNativeFlowWorld(t)
	w.do(func() { w.a.openAgent("vps", "demo") })
	w.wait(t, "native card", func() bool { return w.tt.HasText(T("native.open")) })
	w.click(t, T("native.open"))
	w.wait(t, "prepare view", func() bool { return w.tt.HasText(T("native.stop_and_onboard")) })
	hasAll(t, w.tt.Texts(), T("native.daily_title"), T("native.session"),
		T("native.onboard_title"), T("native.onboard_service_warn", "units", "demo.service"))

	// Without offsite backup, clicking stop-and-onboard hits the gate BEFORE stopping demo.service.
	w.click(t, T("native.stop_and_onboard"))
	w.wait(t, "backup gate", func() bool { return w.tt.HasText(T("native.backup_needed")) })
	if b, _ := os.ReadFile(filepath.Join(w.home, "unit-state")); strings.TrimSpace(string(b)) != "active" {
		t.Fatalf("service was stopped despite gate block: %q", b)
	}

	// Accept risk once: proves owner, stops service BEFORE onboard runs, runs onboard, shows restore button.
	w.click(t, T("native.accept_risk"))
	w.click(t, T("native.continue_once"))
	asked := w.answerOwner(t, func() bool { return w.a.native != nil && !w.a.native.running })
	if asked == 0 {
		t.Fatal("pausing service did not verify owner passphrase")
	}
	if b, _ := os.ReadFile(filepath.Join(w.home, "seen-during-onboard")); strings.TrimSpace(string(b)) != "inactive" {
		t.Fatalf("service was not inactive during onboarding: %q", b)
	}
	w.wait(t, "restore button", func() bool { return w.tt.HasText(T("native.restore_service")) })
	hasAll(t, w.tt.Texts(), T("native.exited", "code", 0), T("native.restore_service"))

	// One-click restore: starts service and separately reports start + health verification.
	w.click(t, T("native.restore_service"))
	w.wait(t, "service restored", func() bool {
		n := w.a.natives["vps/demo"]
		return n != nil && !n.restoreBusy && n.restoreRes != nil
	})
	if b, _ := os.ReadFile(filepath.Join(w.home, "unit-state")); strings.TrimSpace(string(b)) != "active" {
		t.Fatalf("service not active after restore: %q", b)
	}
	hasAll(t, w.tt.Texts(), T("native.service_started", "units", "demo.service"),
		T("health.after", "state", T("service.running")), "本次检查通过")

	// Second pass where starting fails to stay active: command execution and health verdict stay separate.
	if err := os.WriteFile(filepath.Join(w.home, "unit-broken"), []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.click(t, T("native.to_prepare"))
	w.wait(t, "back to prepare with active service", func() bool { return w.tt.HasText(T("native.stop_and_onboard")) })
	w.click(t, T("native.stop_and_onboard"))
	w.wait(t, "gate again (not saved)", func() bool { return w.tt.HasText(T("native.backup_needed")) })
	w.click(t, T("native.accept_risk"))
	w.click(t, T("native.continue_once"))
	w.wait(t, "onboard done", func() bool {
		return w.a.native != nil && !w.a.native.running && w.tt.HasText(T("native.restore_service"))
	})
	w.click(t, T("native.restore_service"))
	w.wait(t, "broken restore verified", func() bool {
		n := w.a.natives["vps/demo"]
		return n != nil && !n.restoreBusy && n.restoreRes != nil && n.restoreRes.Verification != nil
	})
	hasAll(t, w.tt.Texts(), T("native.service_started", "units", "demo.service"),
		T("health.after", "state", T("service.failed")), "问题仍在")
}

func TestNativeDisconnectDoesNotStallUI(t *testing.T) {
	w := newNativeFlowWorld(t)
	prof := strings.Replace(nativeTestProfile, "native_session_cli: [/bin/echo, session-done]", "native_session_cli: [/bin/sleep, \"5\"]", 1)
	if err := os.WriteFile(filepath.Join(w.a.svc.Dirs.Data, "profiles/demo.yaml"), []byte(prof), 0o600); err != nil {
		t.Fatal(err)
	}
	origOwner := w.a.svc.OwnerAuth.Passphrase
	w.a.svc.OwnerAuth.Passphrase = func(string) (string, error) { return w.pass, nil }
	if _, err := w.a.svc.Enroll(context.Background(), "vps", service.EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatal(err)
	}
	w.a.svc.OwnerAuth.Passphrase = origOwner
	w.do(func() { w.a.openAgent("vps", "demo") })
	w.wait(t, "native card", func() bool { return w.tt.HasText(T("native.open")) })
	w.click(t, T("native.open"))
	w.wait(t, "prepare session", func() bool { return w.tt.HasText(T("native.session")) })
	w.click(t, T("native.session"))
	w.wait(t, "backup gate", func() bool { return w.tt.HasText(T("native.backup_needed")) })
	w.click(t, T("native.accept_risk"))
	w.click(t, T("native.continue_once"))
	w.answerOwner(t, func() bool { return w.tt.HasText(T("native.disconnect")) })
	t0 := time.Now()
	w.click(t, T("native.disconnect"))
	if elapsed := time.Since(t0); elapsed >= 2*time.Second {
		t.Fatalf("disconnect click took %v", elapsed)
	}
	w.do(func() {
		if !w.tt.HasText(T("native.disconnected")) {
			t.Fatalf("UI did not switch to disconnected immediately: %q", w.tt.Texts())
		}
	})
	w.wait(t, "native worker finished", func() bool { return w.a.native != nil && !w.a.native.running })
}
