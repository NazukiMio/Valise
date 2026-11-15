package isolated

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestSourceHomeNeverExposesHostHome(t *testing.T) {
	const guestHome = "/source/account"
	w, err := NewWorkspaceForHome(t.TempDir(), guestHome)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.SetProgramPaths([]string{".agent/program", ".runtime"}); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(w.dir, "etc", "passwd")); err != nil || !strings.Contains(string(b), ":"+guestHome+":") {
		t.Fatal("passwd changed the recorded prefix", err)
	}
	spec := Spec{Home: guestHome, WorkingDir: guestHome + "/.agent", Argv: []string{guestHome + "/.agent/program/agent"}}
	if runtime.GOOS == "linux" {
		args, err := arguments(w, spec)
		if err != nil {
			t.Fatal(err)
		}
		for _, rel := range w.programs {
			sequence := []string{"--ro-bind", filepath.Join(w.ProgramDir(), rel), guestHome + "/" + rel}
			if !strings.Contains(strings.Join(args, "\x00"), strings.Join(sequence, "\x00")) {
				t.Fatal("programs were not mounted read-only", args)
			}
		}
		for i, arg := range args {
			if slices.Contains([]string{"--bind", "--ro-bind"}, arg) && args[i+1] == guestHome {
				t.Fatal("recorded host HOME was exposed")
			}
		}
	}
	spec.Home = Home
	if _, err := arguments(w, spec); err != ErrWorkspace {
		t.Fatal("descriptor could replace workspace HOME", err)
	}
}

func TestWorkspaceRejectsReservedHomesAndOverlappingPrograms(t *testing.T) {
	for _, home := range []string{"", "/", "/usr", "/usr/local/user", "/proc/account", "/tmp/account", "/valise/user", "/run", "/home/../etc", "/home/user:bin", "/home/user\n"} {
		if w, err := NewWorkspaceForHome(t.TempDir(), home); err != ErrWorkspace || w != nil {
			t.Fatal("accepted reserved or unclean HOME", home, err)
		}
	}
	for _, programs := range [][]string{{"../escape"}, {"/absolute"}, {"."}, {"program", "program/subdir"}, {"program", "program"}, {"program/subdir", "program"}} {
		w, err := NewWorkspace(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if w.SetProgramPaths(programs) != ErrWorkspace {
			t.Fatal("accepted invalid program directories", programs)
		}
	}
}

func TestProgramMountCannotFollowAHostSymlink(t *testing.T) {
	for _, programs := range []bool{false, true} {
		w, err := NewWorkspace(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if err := w.SetProgramPaths([]string{"nested/program"}); err != nil {
			t.Fatal(err)
		}
		base := w.HomeDir()
		if programs {
			base = w.ProgramDir()
		}
		// Replacing a parent after preparation must not make bwrap follow an
		// external source or mount over an unintended destination.
		parent := filepath.Join(base, "nested")
		if err := os.Rename(parent, parent+"-original"); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(t.TempDir(), parent); err != nil {
			t.Fatal(err)
		}
		if w.validateProgramMounts() != ErrWorkspace {
			t.Fatal("accepted a program mount through an external parent")
		}
	}
}
