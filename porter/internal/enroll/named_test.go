package enroll

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	coreenroll "valise/core/enroll"
	"valise/core/profile"
)

func mkfile(t *testing.T, p string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Named instances (docs/PROFILES.md 2, 多实例): every matching directory
// with a marker is one instance; the harness's home variable stands for
// ${HOME}; a directory holding only skills/ and logs/ is no instance; one
// directory is one instance; instances nested inside another are found and
// left out of its walk.
func TestNamedInstances(t *testing.T) {
	home := t.TempDir()
	alt := t.TempDir()
	p := &profile.Profile{ID: "demo", Detect: &profile.Detect{
		StateDirCandidates: []string{"${HOME}/.demo"}, MarkersAny: []string{"demo.json"}, HomeEnv: "DEMO_HOME",
		Named: []profile.Named{{Glob: "${HOME}/.demo-*", Env: map[string]string{"DEMO_PROFILE": "${NAME}"}}, {Glob: "${HOME}/.demo/profiles/*", Env: map[string]string{"DEMO_HOME": "${STATE_DIR}"}}}}}
	mkfile(t, filepath.Join(home, ".demo/demo.json"))
	mkfile(t, filepath.Join(home, ".demo-work/demo.json"))
	mkfile(t, filepath.Join(home, ".demo-skills/skills/a/SKILL.md")) // no marker
	mkfile(t, filepath.Join(home, ".demo/profiles/bot/demo.json"))
	mkfile(t, filepath.Join(home, ".demo/profiles/bot/memory/m.md"))
	mkfile(t, filepath.Join(home, ".demo/memory/a.md"))
	os.Symlink(filepath.Join(home, ".demo-work"), filepath.Join(home, ".demo-alias"))

	e := &Env{Home: home, Vars: []string{"PATH=/usr/bin:/bin"}}
	got := map[string]string{}
	for _, c := range detectAll(context.Background(), e, p) {
		got[c.Name] = c.StateDir
	}
	if len(got) != 3 || got[""] != filepath.Join(home, ".demo") || got["work"] != filepath.Join(home, ".demo-work") || got["bot"] != filepath.Join(home, ".demo/profiles/bot") {
		t.Fatalf("%v", got)
	}

	nested := NestedInstances(e, p, "demo", filepath.Join(home, ".demo"))
	if len(nested) != 1 || nested[0] != realPath(filepath.Join(home, ".demo/profiles/bot")) {
		t.Fatalf("nested %v", nested)
	}
	var walked []string
	roots := []coreenroll.Root{{ID: "state", Path: filepath.Join(home, ".demo"), Exists: true}}
	Walk(roots, nested, profile.NewClassifier(p, nil), func(it Item) { walked = append(walked, it.Rel) }, func(string, string) {})
	for _, w := range walked {
		if filepath.Dir(w) == "profiles/bot" || w == "profiles/bot/demo.json" || w == "profiles/bot/memory/m.md" {
			t.Fatalf("walked into the nested instance: %v", walked)
		}
	}
	if len(walked) != 2 {
		t.Fatalf("%v", walked)
	}

	// A state dir reached through a symlink is walked at its real path;
	// the nested instance is still left out.
	link := filepath.Join(t.TempDir(), "link")
	os.Symlink(filepath.Join(home, ".demo"), link)
	walked = nil
	roots = []coreenroll.Root{{ID: "state", Path: link, Resolved: filepath.Join(home, ".demo"), Exists: true}}
	Walk(roots, NestedInstances(e, p, "demo", link), profile.NewClassifier(p, nil), func(it Item) { walked = append(walked, it.Rel) }, func(string, string) {})
	if len(walked) != 2 {
		t.Fatalf("through a symlink: %v", walked)
	}

	// The harness's home variable moves ${HOME}.
	mkfile(t, filepath.Join(alt, ".demo/demo.json"))
	e.Vars = append(e.Vars, "DEMO_HOME="+alt)
	cs := detectAll(context.Background(), e, p)
	if len(cs) != 1 || cs[0].StateDir != filepath.Join(alt, ".demo") {
		t.Fatalf("%+v", cs)
	}
}
