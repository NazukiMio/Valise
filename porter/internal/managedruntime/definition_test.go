package managedruntime

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"valise/core/identity"
	"valise/porter/internal/isolated"
)

func definitionFixture(t *testing.T) (Definition, *isolated.Workspace, isolated.Spec, []isolated.DataBinding) {
	t.Helper()
	base := t.TempDir()
	w, err := isolated.NewWorkspaceForHome(base, "/home/fixture")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.SetProgramPaths([]string{"app"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.ProgramDir(), "app", "gateway"), []byte("private program"), 0700); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(base, "live state%slot$")
	if err := os.Mkdir(data, 0700); err != nil {
		t.Fatal(err)
	}
	data, err = filepath.EvalSymlinks(data)
	if err != nil {
		t.Fatal(err)
	}
	spec := isolated.Spec{Home: "/home/fixture", Argv: []string{"/home/fixture/app/gateway", "literal $HOME %n"}, WorkingDir: "/home/fixture/state", Path: []string{"/home/fixture/app"}}
	bindings := []isolated.DataBinding{{Host: data, Guest: "/home/fixture/state"}}
	d, err := compileDefinition(identity.AgentRef{ServerUID: "server", InstallID: "fixture", InstanceID: "fixture", Generation: "generation"}, []string{"--unshare-all", "--share-net", "--disable-userns", "--clearenv", "--bind", data, bindings[0].Guest, "--", spec.Argv[0], spec.Argv[1]})
	if err != nil {
		t.Fatal(err)
	}
	return d, w, spec, bindings
}

func TestDefinitionIsFixedEscapedUserUnitWithoutPorterDaemon(t *testing.T) {
	d, _, _, _ := definitionFixture(t)
	for _, required := range []string{"ExecStart=\"/usr/bin/bwrap\"", "\"--share-net\"", "\"--unshare-all\"", "\"--disable-userns\"", "\"--clearenv\"", "literal $$HOME %%n", "StandardOutput=null", "StandardError=null", "NoNewPrivileges=yes", "KillMode=control-group"} {
		if !strings.Contains(d.Content(), required) {
			t.Fatal("required policy missing", required)
		}
	}
	for _, forbidden := range []string{"valise-porter", "CapabilityBoundingSet", "AmbientCapabilities", "/bin/sh", "-c ", "0.0.0.0"} {
		if strings.Contains(d.Content(), forbidden) {
			t.Fatal("unexpected service behavior", forbidden)
		}
	}
	if !validDefinition(d) || d.Digest() == "" {
		t.Fatal("definition lost exact identity")
	}
	if _, err := systemdWord("hidden\nExecStart=/bin/false"); err == nil {
		t.Fatal("unit directive injection accepted")
	}
}

func TestManagedDefinitionRejectsProgramDataCollisionAndHostShell(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux OS mount metadata is verified by the real runtime lab")
	}
	_, w, spec, bindings := definitionFixture(t)
	agent := identity.AgentRef{ServerUID: "server", InstallID: "fixture", InstanceID: "fixture", Generation: "generation"}
	if _, err := Compile(agent, w, spec, bindings); err != nil {
		t.Fatal("valid managed layout refused", err)
	}
	for _, guest := range []string{"/home/fixture", "/home/fixture/app", "/home/fixture/app/cache", "/tmp/state"} {
		bad := append([]isolated.DataBinding{}, bindings...)
		bad[0].Guest = guest
		if _, err := Compile(agent, w, spec, bad); err == nil {
			t.Fatal("unsafe data root accepted", guest)
		}
	}
	for _, host := range []string{w.ProgramDir(), w.HomeDir(), filepath.Dir(w.HomeDir())} {
		canonical, err := filepath.EvalSymlinks(host)
		if err != nil {
			t.Fatal(err)
		}
		bad := append([]isolated.DataBinding{}, bindings...)
		bad[0].Host = canonical
		if _, err := Compile(agent, w, spec, bad); err == nil {
			t.Fatal("private programme/control tree exposed as writable data")
		}
	}
	spec.Argv = []string{"/bin/sh", "-c", "anything"}
	if _, err := Compile(agent, w, spec, bindings); err == nil {
		t.Fatal("host interpreter accepted")
	}
}

func TestManagedQueryNeverInitializesPrivateState(t *testing.T) {
	d, _, _, _ := definitionFixture(t)
	home := t.TempDir()
	if _, err := (Controller{Home: home}).Read(context.Background(), d); err == nil {
		t.Fatal("missing service fabricated a state")
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatal("query created runtime state", err)
	}
}

func TestUnitFileOwnershipRefusesForeignSymlinksAndContents(t *testing.T) {
	d, _, _, _ := definitionFixture(t)
	c := Controller{Home: t.TempDir()}
	if err := os.Chmod(c.Home, 0700); err != nil {
		t.Fatal(err)
	}
	if err := c.directories(".config", "systemd", "user"); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(t.TempDir(), "foreign")
	if err := os.WriteFile(foreign, []byte(d.content), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(foreign, c.path(d)); err != nil {
		t.Fatal(err)
	}
	if err := c.checkFile(d, d.digest); err == nil {
		t.Fatal("symlink accepted as owned unit")
	}
	if err := os.Remove(c.path(d)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.path(d), []byte("foreign content"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.checkFile(d, d.digest); err == nil {
		t.Fatal("foreign contents accepted")
	}
}
