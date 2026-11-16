package install

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"valise/core/enroll"
	"valise/core/profile"
	"valise/core/valise"
	porterenroll "valise/porter/internal/enroll"
)

// The instance shell is the user's own shell as an interactive login
// shell; a POSIX shell puts the instance's PATH back after its profile.
// The command is fixed: the shell's path is its only variable part.
func TestLoginShellCommand(t *testing.T) {
	for _, sh := range []string{"/bin/sh", "/bin/bash", "/usr/bin/zsh", "/bin/dash"} {
		got := loginShell(sh)
		if len(got) != 5 || got[0] != sh || got[1] != "-l" || got[2] != "-c" || got[4] != sh ||
			!strings.Contains(got[3], `PATH="$VALISE_INSTANCE_PATH:$PATH"`) || !strings.HasSuffix(got[3], `exec "$0" -i`) {
			t.Fatalf("%s: %q", sh, got)
		}
	}
	if got := strings.Join(loginShell("/usr/bin/fish"), " "); got != "/usr/bin/fish -l -i" {
		t.Fatalf("fish: %q", got)
	}
	if got := strings.Join(loginShell("/opt/odd/shell"), " "); got != "/opt/odd/shell -i" {
		t.Fatalf("unknown shell: %q", got)
	}
}

func TestNativeInfoAndModes(t *testing.T) {
	home := t.TempDir()
	lock, err := os.CreateTemp(t.TempDir(), "lock-*")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()

	// Both native_cli and native_session_cli.
	pBoth := profile.Profile{
		ID: "demo", ProfileVersion: 1, Status: profile.StatusDraft,
		NativeCLI:        profile.Argv{"/bin/echo", "onboard"},
		NativeSessionCLI: profile.Argv{"/bin/echo", "tui"},
		NativeHints:      []string{"hint-1"},
	}
	if _, err := porterenroll.WriteRecord(home, enroll.Record{InstanceID: "demo", ProfileID: "demo", ProfileVersion: 1}, pBoth); err != nil {
		t.Fatal(err)
	}
	info, err := NativeInfo(home, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if !info.Shell || !info.Session || !slices.Equal(info.Argv, []string{"/bin/echo", "onboard"}) || !slices.Equal(info.SessionArgv, []string{"/bin/echo", "tui"}) {
		t.Fatalf("unexpected info: %+v", info)
	}
	ctx := context.Background()
	cmd, disp, err := NativeCommand(ctx, home, "demo", ModeSession, lock)
	if err != nil || cmd == nil || !slices.Equal(disp, []string{"/bin/echo", "tui"}) || !slices.Equal(cmd.Args, []string{"/bin/echo", "tui"}) {
		t.Fatalf("session cmd: %+v disp=%v err=%v", cmd, disp, err)
	}
	cmd, disp, err = NativeCommand(ctx, home, "demo", ModeOnboard, lock)
	if err != nil || cmd == nil || !slices.Equal(disp, []string{"/bin/echo", "onboard"}) {
		t.Fatalf("onboard cmd: %+v disp=%v err=%v", cmd, disp, err)
	}
	if _, _, err := NativeCommand(ctx, home, "demo", "bogus", lock); err == nil {
		t.Fatal("accepted bogus native mode")
	}

	// Only native_session_cli (no native_cli).
	home2 := t.TempDir()
	pSess := profile.Profile{
		ID: "sess", ProfileVersion: 1, Status: profile.StatusDraft,
		NativeSessionCLI: profile.Argv{"/bin/cat"},
	}
	if _, err := porterenroll.WriteRecord(home2, enroll.Record{InstanceID: "sess", ProfileID: "sess", ProfileVersion: 1}, pSess); err != nil {
		t.Fatal(err)
	}
	info2, err := NativeInfo(home2, "sess")
	if err != nil || !info2.Shell || !info2.Session || len(info2.Argv) != 0 || !slices.Equal(info2.SessionArgv, []string{"/bin/cat"}) {
		t.Fatalf("session-only info: %+v err=%v", info2, err)
	}
	if _, _, err := NativeCommand(ctx, home2, "sess", ModeOnboard, lock); err == nil {
		t.Fatal("expected native_cli_none when native_cli is absent")
	} else if ue, ok := valise.As(err); !ok || ue.Code != "native_cli_none" {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd, _, err := NativeCommand(ctx, home2, "sess", ModeShell, lock); err != nil || cmd == nil {
		t.Fatalf("shell on session-only profile: %v", err)
	}

	// Only native_cli (no native_session_cli).
	home3 := t.TempDir()
	pOnboard := profile.Profile{
		ID: "onb", ProfileVersion: 1, Status: profile.StatusDraft,
		NativeCLI: profile.Argv{"/bin/echo", "onb"},
	}
	if _, err := porterenroll.WriteRecord(home3, enroll.Record{InstanceID: "onb", ProfileID: "onb", ProfileVersion: 1}, pOnboard); err != nil {
		t.Fatal(err)
	}
	info3, err := NativeInfo(home3, "onb")
	if err != nil || !info3.Shell || info3.Session || len(info3.SessionArgv) != 0 {
		t.Fatalf("onboard-only info: %+v err=%v", info3, err)
	}
	if _, _, err := NativeCommand(ctx, home3, "onb", ModeSession, lock); err == nil {
		t.Fatal("expected native_session_none when native_session_cli is absent")
	} else if ue, ok := valise.As(err); !ok || ue.Code != "native_session_none" {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = filepath.Base(home)
}
