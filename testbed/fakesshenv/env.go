// Package fakesshenv prepares fake-ssh for tests: it builds fakessh and the
// porter into t.TempDir(), points FAKESSH_ROOT at a fresh root and creates
// fake hosts. It never modifies PATH; callers pass Env.SSH as ssh_binary.
package fakesshenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// Env is one fake-ssh world.
type Env struct {
	t testing.TB
	// Root is $FAKESSH_ROOT.
	Root string
	// SSH is the fakessh binary to use as ssh_binary.
	SSH    string
	porter string
}

// RepoRoot returns the repository root.
func RepoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// New builds fakessh and sets FAKESSH_ROOT for the rest of the test, so the
// test must not run in parallel with others that use fake-ssh.
func New(t testing.TB) *Env {
	t.Helper()
	bin := t.TempDir()
	e := &Env{t: t, Root: t.TempDir(), SSH: filepath.Join(bin, "ssh")}
	build(t, filepath.Join(RepoRoot(), "testbed"), e.SSH, "./fakessh")
	t.Setenv("FAKESSH_ROOT", e.Root)
	return e
}

// AddHost creates a fake host and returns its HOME.
func (e *Env) AddHost(alias string) string {
	e.t.Helper()
	home := filepath.Join(e.Root, "hosts", alias, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		e.t.Fatal(err)
	}
	return home
}

// Porter returns a porter binary built for the host system.
func (e *Env) Porter() string {
	e.t.Helper()
	if e.porter == "" {
		e.porter = filepath.Join(e.t.TempDir(), "valise-porter")
		build(e.t, filepath.Join(RepoRoot(), "porter"), e.porter, ".")
	}
	return e.porter
}

// InstallPorter copies the porter to ~/.local/bin/valise-porter of a fake
// host, the first entry of the probe chain, and returns that path.
func (e *Env) InstallPorter(alias string) string {
	e.t.Helper()
	return e.InstallPorterAt(alias, ".local/bin/valise-porter")
}

// InstallPorterAt copies the porter to a HOME-relative path of a fake host.
func (e *Env) InstallPorterAt(alias, rel string) string {
	e.t.Helper()
	data, err := os.ReadFile(e.Porter())
	if err != nil {
		e.t.Fatal(err)
	}
	dst := filepath.Join(e.Root, "hosts", alias, "home", rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(dst, data, 0o755); err != nil {
		e.t.Fatal(err)
	}
	return dst
}

func build(t testing.TB, dir, out, pkg string) {
	t.Helper()
	cmd := exec.Command("go", "build", "-o", out, pkg)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build %s in %s: %v\n%s", pkg, dir, err, b)
	}
}
