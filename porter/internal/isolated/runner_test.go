package isolated

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestMountPolicyAndWorkspaceValidation(t *testing.T) {
	w, err := NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	spec := Spec{Argv: []string{"/usr/bin/true"}, WorkingDir: Home, Path: []string{Home + "/program/bin"}}
	// Actual OS mount metadata is Linux-specific. The complete policy checks
	// run on the real VM alongside TestLinuxBoundary, not a fake Linux tree.
	if runtime.GOOS == "linux" {
		args, err := arguments(w, spec)
		if err != nil {
			t.Fatal(err)
		}
		for _, mandatory := range []string{"--unshare-all", "--unshare-user", "--disable-userns", "--clearenv", "--die-with-parent", "--new-session"} {
			if !slices.Contains(args, mandatory) {
				t.Fatal("missing boundary", mandatory)
			}
		}
		for i, a := range args {
			if a == "--share-net" || a == "--bind" && args[i+1] != w.HomeDir() || a == "--ro-bind" && slices.Contains([]string{"/", "/usr", "/usr/local", os.Getenv("HOME"), "/etc", "/run", "/tmp"}, args[i+1]) {
				t.Fatal("exposed host state", args)
			}
		}
		if strings.Contains(strings.Join(args, "\x00"), "SSH_AUTH_SOCK") {
			t.Fatal("inherited login session")
		}
	}
	for _, bad := range []Spec{
		{Argv: []string{"true"}, WorkingDir: "/"},
		{Argv: []string{"true"}, WorkingDir: Home + "/../elsewhere"},
		{Argv: []string{"true"}, WorkingDir: Home, Path: []string{"/usr/local/bin"}},
		{WorkingDir: Home},
	} {
		if _, err := arguments(w, bad); err == nil {
			t.Fatal("accepted unscoped spec", bad)
		}
	}
	// A builder must copy data, never hardlink active data into the candidate.
	original := filepath.Join(t.TempDir(), "active")
	if err := os.WriteFile(original, []byte("active fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(original, filepath.Join(w.HomeDir(), "alias")); err != nil {
		t.Fatal(err)
	}
	if _, err := arguments(w, spec); err != ErrWorkspace {
		t.Fatal("accepted hardlink to active data", err)
	}
}

func TestTaskRequiresDeadline(t *testing.T) {
	if err := Run(context.Background(), nil, Spec{}); err != ErrDeadline {
		t.Fatal("unbounded task accepted", err)
	}
}
