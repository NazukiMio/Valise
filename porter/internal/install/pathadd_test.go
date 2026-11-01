package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPathAdd(t *testing.T) {
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, ".profile"), []byte("# existing"), 0o644)
	file, lines, added, err := PathAdd(home, "/bin/bash", "${HOME}/.demo/bin", "demo")
	if err != nil || !added || file != filepath.Join(home, ".profile") || len(lines) != 2 {
		t.Fatalf("%s %v %v %v", file, lines, added, err)
	}
	if _, _, added, err := PathAdd(home, "/bin/bash", "${HOME}/.demo/bin", "demo"); err != nil || added {
		t.Fatalf("added twice: %v", err)
	}
	b, _ := os.ReadFile(file)
	if !strings.HasPrefix(string(b), "# existing\n# Added by Valise (demo)") || strings.Count(string(b), "export PATH") != 1 {
		t.Fatalf("%q", b)
	}
	if file, _, _, _ := PathAdd(home, "/usr/bin/zsh", "${HOME}/.demo/bin", "demo"); file != filepath.Join(home, ".zprofile") {
		t.Fatal(file)
	}
	os.WriteFile(filepath.Join(home, ".bash_profile"), nil, 0o644)
	if file, _, _, _ := PathAdd(home, "bash", "${HOME}/.x/bin", "x"); file != filepath.Join(home, ".bash_profile") {
		t.Fatal(file)
	}
	for _, bad := range []string{"/usr/local/bin", "${HOME}/../etc", "${HOME}/a\"b", "${HOME}/$(x)", home} {
		if _, _, _, err := PathAdd(home, "bash", bad, "x"); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}
