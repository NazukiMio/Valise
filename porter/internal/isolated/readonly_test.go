package isolated

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestReadonlyPolicyUsesEphemeralHomeAndNeverCreatesActiveMountpoints(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires actual Linux OS mount metadata; boundary runs in the lab")
	}
	w, err := NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := w.SetProgramPaths([]string{"state/program"}); err != nil {
		t.Fatal(err)
	}
	if err := PrepareNetwork(w); err != nil {
		t.Fatal(err)
	}
	data := t.TempDir()
	if err := os.Chmod(data, 0700); err != nil {
		t.Fatal(err)
	}
	spec := Spec{Argv: []string{Home + "/state/program/check", "--bind"}, WorkingDir: Home + "/state"}
	roots := []DataBinding{{Host: data, Guest: Home + "/state"}}
	if _, err := ReadonlyAttachedArguments(w, spec, roots); err != ErrWorkspace {
		t.Fatal("query created a programme mountpoint", err)
	}
	if _, err := os.Stat(filepath.Join(data, "program")); !os.IsNotExist(err) {
		t.Fatal("query repaired active data", err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(data, "program")); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadonlyAttachedArguments(w, spec, roots); err != ErrWorkspace {
		t.Fatal("query accepted an aliased programme mountpoint", err)
	}
	if err := os.Remove(filepath.Join(data, "program")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(data, "program"), 0700); err != nil {
		t.Fatal(err)
	}
	args, err := ReadonlyAttachedArguments(w, spec, roots)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, "\x00")
	for _, required := range [][]string{{"--ro-bind", data, Home + "/state"}, {"--perms", "0700", "--tmpfs", Home}} {
		if !strings.Contains(joined, strings.Join(required, "\x00")) {
			t.Fatal("missing read-only boundary", required)
		}
	}
	if slices.Contains(args, w.HomeDir()) || !slices.Equal(args[len(args)-3:], []string{"--", spec.Argv[0], "--bind"}) {
		t.Fatal("query retained persistent HOME or changed programme arguments", args)
	}
}
