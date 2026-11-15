package environments

import (
	"archive/tar"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"valise/core/vault"
)

func TestBoundProgrammeArchiveOnlyCopiesVerifiedProgrammeAndRejectsChangedRuntime(t *testing.T) {
	s, agent, in, body := bindingFixture(t)
	runtime := &vault.RuntimeRecord{Format: vault.FormatRuntime, OS: in.Program.OS, Arch: in.Program.Arch, Home: vault.RuntimeRoot{Path: in.Layout.Home},
		Installation: &vault.RuntimeInstallation{ProfileDoc: &in.Profile}, Dependencies: []vault.RuntimeDependency{{Role: "program", Path: in.Layout.Home + "/" + in.Program.Files[0].Path, SHA256: in.Program.Files[0].SHA256}}}
	original := originalInput(in, body)
	original.Runtime = runtime
	w, binding, err := s.BuildOriginal(context.Background(), agent, original)
	if err != nil {
		t.Fatal(err)
	}
	initialRegistry(t, s, agent)
	if err := os.MkdirAll(filepath.Join(s.Home, ".fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Home, ".fixture/SOUL.md"), []byte("active data must never enter archive"), 0600); err != nil {
		t.Fatal(err)
	}
	archive, err := s.ArchiveBound(context.Background(), t.TempDir(), agent, binding.Ref, Digest(binding), runtime)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.File.Close()
	tr := tar.NewReader(archive.File)
	for _, file := range binding.Program.Files {
		h, err := tr.Next()
		if err != nil || h.Name != file.Path || h.Size != file.Size {
			t.Fatal("archive escaped fixed programme inventory", h, err)
		}
		payload, err := io.ReadAll(tr)
		if err != nil || vault.SHA256Hex(payload) != file.SHA256 {
			t.Fatal("programme archive bytes changed", err)
		}
	}
	if _, err := tr.Next(); err != io.EOF {
		t.Fatal("active or unlisted data entered archive", err)
	}
	wrong := *runtime
	wrong.Dependencies = []vault.RuntimeDependency{{Role: "program", SHA256: "wrong historical dependency"}}
	if _, err := s.ArchiveBound(context.Background(), t.TempDir(), agent, binding.Ref, Digest(binding), &wrong); err == nil {
		t.Fatal("current code substituted for another recorded runtime")
	}
	if err := os.WriteFile(filepath.Join(w.ProgramDir(), filepath.FromSlash(binding.Program.Files[0].Path)), []byte("changed"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ArchiveBound(context.Background(), t.TempDir(), agent, binding.Ref, Digest(binding), runtime); err == nil {
		t.Fatal("changed private programme copied")
	}
}
