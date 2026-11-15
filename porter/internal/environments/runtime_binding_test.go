package environments

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"valise/core/profile"
)

func runtimeBindingFixture(t *testing.T) BuildInput {
	t.Helper()
	in := fixtureInput(t, []byte("private program"))
	service := profile.ServiceUnit{Scope: "user", Unit: "fixture.service"}
	in.Profile.Service = &profile.Service{Candidates: []profile.ServiceUnit{service}}
	in.Profile.Environments.RuntimeExecutable = &profile.RuntimeExecutable{Service: service, Name: "engine"}
	in.Profile.Environments.StartArgv = profile.Argv{"${RUNTIME_EXECUTABLE}", "${STATE_DIR}/program/agent"}
	in.Profile.Environments.Path = []string{"${RUNTIME_DIR}"}
	in.Layout.RuntimeExecutable = in.Layout.Home + "/.runtime/bin/engine"
	in.Program.Files = append(in.Program.Files, ProgramFile{Path: ".runtime/bin/engine", Size: 14, SHA256: hashFixture([]byte("private engine")), Mode: 0o700})
	setProgramArchive(t, &in, map[string][]byte{".fixture/program/agent": []byte("private program"), ".runtime/bin/engine": []byte("private engine")}, false)
	in.Target.Source.ProfileDigest = Digest(in.Profile)
	in.Target.Source.LayoutDigest = LayoutDigest(in.Layout, in.Overrides)
	return in
}

func TestRuntimeBindingCopiesOnlyPrivateExecutableAndExpandsFixedEntry(t *testing.T) {
	in := runtimeBindingFixture(t)
	c, err := Build(context.Background(), t.TempDir(), in)
	if err != nil {
		t.Fatal(err)
	}
	if c.Spec.Argv[0] != in.Layout.RuntimeExecutable || c.Spec.Path[0] != in.Layout.Home+"/.runtime/bin" {
		t.Fatal("verified runtime was replaced by shared PATH")
	}
	if b, err := os.ReadFile(filepath.Join(c.Workspace.ProgramDir(), ".runtime/bin/engine")); err != nil || string(b) != "private engine" {
		t.Fatal("runtime was not copied privately", err)
	}
	if _, err := os.Stat(filepath.Join(c.Workspace.HomeDir(), ".runtime/bin/engine")); !os.IsNotExist(err) {
		t.Fatal("program appeared in exchangeable data tree")
	}
}

func TestRuntimeBindingCannotBorrowHostOrOverwriteAgentData(t *testing.T) {
	for _, runtime := range []string{"", "/usr/bin/engine", "/source/home/.runtime/bin/unrelated", "/source/home/.fixture/memory/engine"} {
		in := runtimeBindingFixture(t)
		in.Layout.RuntimeExecutable = runtime
		in.Target.Source.LayoutDigest = LayoutDigest(in.Layout, in.Overrides)
		if c, err := Build(context.Background(), t.TempDir(), in); err == nil || c != nil {
			t.Fatal("invalid runtime binding accepted", runtime)
		}
	}
}
