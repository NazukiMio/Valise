package service

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"valise/client/minisign/minisigntest"
	"valise/testbed/fakesshenv"
)

func serviceWorld(t *testing.T, withService bool, initialLoad, initialActive string) (*Service, string) {
	t.Helper()
	e := fakesshenv.New(t)
	home := e.AddHost("box")
	e.InstallPorter("box")
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
	write(filepath.Join(home, ".local/bin/demo"), `#!/bin/sh
if [ "$1" = "install-svc" ]; then
  echo "installing demo service"
  if [ ! -f "$HOME/install-noop" ]; then
    echo loaded > "$HOME/unit-load"
    echo active > "$HOME/unit-active"
  fi
  exit 0
fi
echo demo 1.0
`)
	write(filepath.Join(home, "unit-load"), initialLoad+"\n")
	write(filepath.Join(home, "unit-active"), initialActive+"\n")
	write(filepath.Join(home, ".local/bin/systemctl"), `#!/bin/sh
shift # --user
act="$1"
echo "$act" >> "$HOME/systemctl.log"
case "$act" in
show)
  load=$(cat "$HOME/unit-load")
  active=$(cat "$HOME/unit-active")
  printf 'LoadState=%s\nActiveState=%s\nUnitFileState=enabled\n' "$load" "$active"
  ;;
is-active)
  cat "$HOME/unit-active"
  ;;
stop)
  echo inactive > "$HOME/unit-active"
  ;;
reset-failed)
  ;;
start)
  if [ -f "$HOME/unit-fail" ]; then
    echo failed > "$HOME/unit-active"
  else
    echo active > "$HOME/unit-active"
  fi
  ;;
restart)
  if [ -f "$HOME/unit-fail" ]; then
    echo failed > "$HOME/unit-active"
  else
    echo active > "$HOME/unit-active"
  fi
  ;;
esac
`)
	s := newService(t, e.SSH)
	s.RiskAccepted = true
	prof := demoProfile
	if withService {
		prof += `service:
  candidates: [ { scope: user, unit: demo.service } ]
  install_argv: [demo, install-svc]
`
	}
	write(filepath.Join(s.Dirs.Data, "profiles/demo.yaml"), prof)
	if _, err := s.Enroll(context.Background(), "box", EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatal(err)
	}
	return s, home
}

func TestControlInstanceServiceStartStopRestartAndHonestState(t *testing.T) {
	oldSettle, oldPoll := serviceControlSettle, serviceControlPoll
	serviceControlSettle, serviceControlPoll = 200*time.Millisecond, 20*time.Millisecond
	defer func() { serviceControlSettle, serviceControlPoll = oldSettle, oldPoll }()

	s, home := serviceWorld(t, true, "loaded", "active")
	ctx := context.Background()

	info, err := s.ServiceStatus(ctx, "box", "demo")
	if err != nil {
		t.Fatal(err)
	}
	if !info.HasUnit || info.Unit != "demo.service" || info.Active != "active" || len(info.InstallArgv) != 2 {
		t.Fatalf("unexpected initial ServiceStatus: %+v", info)
	}

	// Stop
	info, err = s.ControlInstanceService(ctx, "box", "demo", "stop")
	if err != nil || info.Active != "inactive" {
		t.Fatalf("stop: %+v %v", info, err)
	}

	// Start
	info, err = s.ControlInstanceService(ctx, "box", "demo", "start")
	if err != nil || info.Active != "active" {
		t.Fatalf("start: %+v %v", info, err)
	}

	// Restart
	info, err = s.ControlInstanceService(ctx, "box", "demo", "restart")
	if err != nil || info.Active != "active" {
		t.Fatalf("restart: %+v %v", info, err)
	}

	// Honest live state: start exits 0, but unit enters failed state.
	if err := os.WriteFile(filepath.Join(home, "unit-fail"), []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err = s.ControlInstanceService(ctx, "box", "demo", "restart")
	if err != nil {
		t.Fatalf("restart when unit fails after start: %v", err)
	}
	if info.Active != "failed" {
		t.Fatalf("expected honest failed state, got %+v", info)
	}

	// Invalid action rejected cleanly.
	_, err = s.ControlInstanceService(ctx, "box", "demo", "reload")
	wantCode(t, err, "service_action_invalid")

	logBytes, err := os.ReadFile(filepath.Join(home, "systemctl.log"))
	if err != nil {
		t.Fatal(err)
	}
	logText := string(logBytes)
	for _, want := range []string{"stop", "start", "restart"} {
		if !strings.Contains(logText, want) {
			t.Fatalf("expected %q in systemctl log:\n%s", want, logText)
		}
	}
}

func TestControlInstanceServiceRestartFallbackOnOldPorter(t *testing.T) {
	oldSettle, oldPoll := serviceControlSettle, serviceControlPoll
	serviceControlSettle, serviceControlPoll = 200*time.Millisecond, 20*time.Millisecond
	defer func() { serviceControlSettle, serviceControlPoll = oldSettle, oldPoll }()

	s, home := serviceWorld(t, true, "loaded", "active")
	ctx := context.Background()

	// Wrap porter so one service_control(restart) returns RPC CodeInvalidParams
	// (-32602), simulating an older porter that only knew stop/start/status.
	porterPath := filepath.Join(home, ".local/bin/valise-porter")
	realPath := porterPath + ".real"
	if err := os.Rename(porterPath, realPath); err != nil {
		t.Fatal(err)
	}
	wrapper := `#!/bin/sh
if [ "$1" = "rpc" ] && [ -f "$HOME/reject-restart" ]; then
  c=0
  [ -f "$HOME/rpc-count" ] && c=$(cat "$HOME/rpc-count")
  c=$((c + 1))
  echo "$c" > "$HOME/rpc-count"
  if [ "$c" -eq 3 ]; then
    mv "$HOME/reject-restart" "$HOME/reject-restart.used"
    IFS= read -r _ || exit 1
    printf '{"jsonrpc":"2.0","id":1,"result":{"porter_version":"0.0.1","protocol":"1.3","os":"linux","arch":"amd64","home":"%s","tools":[],"pending_ops":[]}}\n' "$HOME"
    IFS= read -r req || exit 1
    echo "$req" > "$HOME/rejected-req.json"
    printf '{"jsonrpc":"2.0","id":2,"error":{"code":-32602,"message":"invalid params"}}\n'
    exit 0
  fi
fi
exec "$HOME/.local/bin/valise-porter.real" "$@"
`
	if err := os.WriteFile(porterPath, []byte(wrapper), 0o700); err != nil {
		t.Fatal(err)
	}
	// Reset systemctl log so we only observe calls made during restart fallback.
	if err := os.WriteFile(filepath.Join(home, "systemctl.log"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "reject-restart"), []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}

	info, err := s.ControlInstanceService(ctx, "box", "demo", "restart")
	if err != nil {
		t.Fatalf("restart fallback failed: %v", err)
	}
	if info.Active != "active" {
		t.Fatalf("expected active after stop+start fallback, got %+v", info)
	}
	reqBytes, err := os.ReadFile(filepath.Join(home, "rejected-req.json"))
	if err != nil || !strings.Contains(string(reqBytes), `"action":"restart"`) {
		t.Fatalf("expected restart RPC to be intercepted, got %q (%v)", string(reqBytes), err)
	}
	logBytes, err := os.ReadFile(filepath.Join(home, "systemctl.log"))
	if err != nil {
		t.Fatal(err)
	}
	logLines := strings.Split(strings.TrimSpace(string(logBytes)), "\n")
	stopIdx, startIdx := -1, -1
	for i, l := range logLines {
		if l == "stop" && stopIdx < 0 {
			stopIdx = i
		}
		if l == "start" && stopIdx >= 0 && startIdx < 0 {
			startIdx = i
		}
		if l == "restart" {
			t.Fatalf("systemctl restart should not have run on fallback, log=%v", logLines)
		}
	}
	if stopIdx < 0 || startIdx < 0 || stopIdx >= startIdx {
		t.Fatalf("expected stop followed by start in fallback, log=%v", logLines)
	}
}

func TestInstallInstanceServiceSeparatesExitAndLiveStatus(t *testing.T) {
	s, home := serviceWorld(t, true, "not-found", "inactive")
	ctx := context.Background()

	// Unloaded unit cannot be started before install.
	_, err := s.ControlInstanceService(ctx, "box", "demo", "start")
	wantCode(t, err, "service_not_installed")

	// Case 1: installer exits 0 but does NOT actually create/load the unit:
	// returned status must honestly report HasUnit=false.
	if err := os.WriteFile(filepath.Join(home, "install-noop"), []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	code, info, err := s.InstallInstanceService(ctx, "box", "demo", InstallIO{
		Stdin: strings.NewReader(""), Stdout: &out, Stderr: &out, PTY: true,
	})
	if err != nil || code != 0 {
		t.Fatalf("InstallInstanceService noop: code=%d err=%v out=%s", code, err, out.String())
	}
	if info.HasUnit || info.LoadState != "not-found" {
		t.Fatalf("expected honest not-found status after noop installer, got %+v", info)
	}

	// Case 2: installer actually installs and activates the unit.
	if err := os.Remove(filepath.Join(home, "install-noop")); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	code, info, err = s.InstallInstanceService(ctx, "box", "demo", InstallIO{
		Stdin: strings.NewReader(""), Stdout: &out, Stderr: &out, PTY: true,
	})
	if err != nil || code != 0 {
		t.Fatalf("InstallInstanceService real: code=%d err=%v out=%s", code, err, out.String())
	}
	if !strings.Contains(out.String(), "installing demo service") {
		t.Fatalf("expected installer output, got %q", out.String())
	}
	if !info.HasUnit || info.LoadState != "loaded" || info.Active != "active" {
		t.Fatalf("expected loaded+active after install, got %+v", info)
	}
}

func TestServiceControlNotDeclared(t *testing.T) {
	s, _ := serviceWorld(t, false, "not-found", "inactive")
	_, err := s.ControlInstanceService(context.Background(), "box", "demo", "start")
	wantCode(t, err, "service_not_declared")
}

func TestServiceStatusReportsStaleLocalOverride(t *testing.T) {
	s, _ := serviceWorld(t, true, "not-found", "inactive")
	// serviceWorld installed local demo.yaml at profile_version: 1.
	// Install an official signed pack containing demo at profile_version: 2 so
	// the local override is stale (effective v1 <= official v2).
	officialV2 := strings.Replace(demoProfile, "profile_version: 1", "profile_version: 2", 1) + `service:
  candidates: [ { scope: user, unit: demo.service } ]
  install_argv: [demo, install-svc]
`
	installTestPack(t, s, minisigntest.New(), officialV2)

	info, err := s.ServiceStatus(context.Background(), "box", "demo")
	if err != nil {
		t.Fatalf("ServiceStatus: %v", err)
	}
	if !info.LocalOverrideStale || info.EffectiveVersion != 1 || info.OfficialVersion != 2 {
		t.Fatalf("expected LocalOverrideStale=true EffectiveVersion=1 OfficialVersion=2, got %+v", info)
	}
}
