package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"valise/client/config"
	"valise/core/valise"
)

// fake-ssh only: this proves Valise's own logic with a stand-in sudo, not
// how a real sudo, sudoers or polkit behaves on a target machine.
const fakeSudoN = `#!/bin/sh
echo sudo >> "$HOME/ran"
if [ "$1" = "-n" ]; then
 shift
 if test -e "$HOME/sudo-password" || { test -e "$HOME/sudo-password-steps" && [ "$1" != true ]; }; then
  echo 'sudo: a password is required' >&2; exit 1
 fi
fi
exec "$@"
`

func takeoverWorld(t *testing.T) (*Service, string) {
	t.Helper()
	s, home := systemWorld(t)
	write(t, filepath.Join(home, ".local/bin/sudo"), fakeSudoN)
	os.Chmod(filepath.Join(home, ".local/bin/sudo"), 0o755)
	s.full.host = "box" // advanced mode the user enabled (the challenge is tested elsewhere)
	if _, err := s.AddServers("box"); err != nil {
		t.Fatal(err)
	}
	return s, home
}

// Only a destructive step asks for an escape pod kept elsewhere; the
// built-in linger and clock steps are not destructive and are not held back.
func TestDestructiveGate(t *testing.T) {
	s, home := takeoverWorld(t)
	ctx := context.Background()
	st := &SystemStatus{User: "alice", CanNTP: "yes", NTP: "no", Timesyncd: "loaded"}
	builtIn := append(lingerSteps("alice", asSudo), clockSteps(st, asSudoN)...)
	builtIn = append(builtIn, lingerSteps("alice", asUser)...)
	if len(builtIn) != 4 {
		t.Fatalf("%+v", builtIn)
	}
	for _, step := range builtIn {
		if step.Destructive {
			t.Fatalf("built-in step marked destructive: %+v", step)
		}
	}
	// The demo agent has no escape pod elsewhere: the gate would refuse.
	if g, err := s.AdvancedGate(ctx, "box", ""); err != nil || g.OK() {
		t.Fatalf("world has an offsite pod: %+v %v", g, err)
	}
	if err := s.destructiveGate(ctx, "box", builtIn); err != nil {
		t.Fatalf("non-destructive steps were checked: %v", err)
	}
	wipe := SystemFixStep{Command: []string{"sudo", "rm", "-r", "/var/lib/example"}, Change: "删除数据", Undo: "不能撤销", Destructive: true}
	err := s.destructiveGate(ctx, "box", append(builtIn, wipe))
	if ue, ok := valise.As(err); !ok || ue.Code != GateCode {
		t.Fatalf("destructive step not checked: %v", err)
	}
	if !(&SystemFix{Steps: []SystemFixStep{wipe}}).Destructive() || (&SystemFix{Steps: builtIn}).Destructive() {
		t.Fatal("Destructive()")
	}
	// Accepting the risk for this one operation lets it through.
	if err := s.destructiveGate(WithRiskAccepted(ctx), "box", []SystemFixStep{wipe}); err != nil {
		t.Fatal(err)
	}
	// The real clock fix runs although there is no escape pod elsewhere.
	write(t, filepath.Join(home, "restart-helps"), "")
	f, err := s.PlanSystemFix(ctx, "box", "demo", FixClock, SystemFixOptions{})
	if err != nil || f.Destructive() {
		t.Fatalf("%+v %v", f, err)
	}
	if _, err := s.RunSystemFix(ctx, "box", "demo", FixClock, f.Digest, SystemFixOptions{}, InstallIO{}); err != nil {
		t.Fatal(err)
	}
}

func TestTakeoverOffAndOn(t *testing.T) {
	s, home := takeoverWorld(t)
	ctx := context.Background()
	write(t, filepath.Join(home, "restart-helps"), "")
	if s.Takeover("box") {
		t.Fatal("takeover on by default")
	}
	// Off: asking for sudo -n changes nothing.
	off, err := s.PlanSystemFix(ctx, "box", "demo", FixClock, SystemFixOptions{NonInteractive: true})
	if err != nil || off.NonInteractive || strings.Join(off.Steps[0].Command, " ") != "sudo timedatectl set-ntp true" {
		t.Fatalf("%+v %v", off, err)
	}
	if err := s.SetTakeover("nowhere", true); err == nil {
		t.Fatal("unknown server accepted")
	}
	if err := s.SetTakeover("box", true); err != nil {
		t.Fatal(err)
	}
	if c, _ := config.Load(s.Dirs.Data); !c.Takeover["box"] || !s.Takeover("box") {
		t.Fatalf("not saved: %+v", c)
	}
	on, err := s.PlanSystemFix(ctx, "box", "demo", FixClock, SystemFixOptions{NonInteractive: true})
	if err != nil {
		t.Fatal(err)
	}
	if !on.NonInteractive || on.Mode != FixAdvanced || strings.Join(on.Steps[0].Command, " ") != "sudo -n timedatectl set-ntp true" ||
		strings.Join(on.Steps[1].Command, " ") != "sudo -n systemctl restart systemd-timesyncd" || on.Steps[0].Undo != "sudo timedatectl set-ntp false" ||
		!strings.Contains(on.Access, "接管模式") || on.Digest == off.Digest || !IsSudo(on.Steps[0].Command) {
		t.Fatalf("%+v", on)
	}
	// The terminal path (no NonInteractive) is unchanged with takeover on.
	if cli, _ := s.PlanSystemFix(ctx, "box", "demo", FixClock, SystemFixOptions{}); cli.NonInteractive || cli.Digest != off.Digest {
		t.Fatalf("%+v", cli)
	}
	res, err := s.RunSystemFix(ctx, "box", "demo", FixClock, on.Digest, SystemFixOptions{NonInteractive: true}, InstallIO{})
	if err != nil {
		t.Fatal(err)
	}
	if res.SudoPassword || !res.Steps[0].Ran || res.Steps[0].Exit != 0 || res.Verification.State != "resolved" || !res.Audited {
		t.Fatalf("%+v %+v", res, res.Verification)
	}
	if r := ran(t, home); !strings.Contains(r, "set-ntp true") || !strings.Contains(r, "sudo") {
		t.Fatal(r)
	}
	if a := audit(t, home); !strings.Contains(a, "sudo -n timedatectl set-ntp true exit 0") || !strings.Contains(a, "(advanced)") {
		t.Fatal(a)
	}
	if err := s.SetTakeover("box", false); err != nil || s.Takeover("box") {
		t.Fatal(err)
	}
}

func TestTakeoverSudoNeedsPassword(t *testing.T) {
	for _, file := range []string{"sudo-password", "sudo-password-steps"} {
		s, home := takeoverWorld(t)
		ctx := context.Background()
		if err := s.SetTakeover("box", true); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(home, file), "")
		f, err := s.PlanSystemFix(ctx, "box", "demo", FixClock, SystemFixOptions{NonInteractive: true})
		if err != nil {
			t.Fatal(err)
		}
		res, err := s.RunSystemFix(ctx, "box", "demo", FixClock, f.Digest, SystemFixOptions{NonInteractive: true}, InstallIO{})
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		if !res.SudoPassword || res.Terminal != "valise health fix box demo clock --advanced" ||
			!strings.Contains(res.Next, "这台服务器的 sudo 需要密码") || !strings.Contains(res.Next, res.Terminal) || res.Verification.State != "remaining" {
			t.Fatalf("%s: %+v %+v", file, res, res.Verification)
		}
		if strings.Contains(ran(t, home), "set-ntp") {
			t.Fatalf("%s: a step ran past sudo: %s", file, ran(t, home))
		}
		if file == "sudo-password" {
			for _, st := range res.Steps {
				if st.Ran || st.Note == "" {
					t.Fatalf("%+v", res.Steps)
				}
			}
		} else if !res.Steps[0].Ran || res.Steps[0].Exit != 1 || res.Steps[1].Ran {
			t.Fatalf("%+v", res.Steps)
		}
		if !strings.Contains(audit(t, home), `"result":"remaining"`) {
			t.Fatal(audit(t, home))
		}
	}
}
