package actionjournal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"valise/core/action"
)

// linkWorld: a state root with a program tree inside it (as an npm prefix
// under the state dir) and one outside it.
func linkWorld(t *testing.T) (root, outside string) {
	t.Helper()
	base, _ := filepath.EvalSymlinks(t.TempDir())
	root = filepath.Join(base, "state")
	outside = filepath.Join(base, "prefix", "lib", "node_modules", "pkg")
	for _, d := range []string{filepath.Join(root, "skills"), filepath.Join(root, "tools", "pkg", "skill"), filepath.Join(outside, "skill")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(root, "config.json"), []byte("{}"), 0o600)
	os.WriteFile(filepath.Join(root, "tools", "pkg", "skill", "SKILL.md"), []byte("inside"), 0o644)
	os.WriteFile(filepath.Join(outside, "skill", "SKILL.md"), []byte("program"), 0o644)
	return root, outside
}

func TestScanLinksClassifies(t *testing.T) {
	root, outside := linkWorld(t)
	os.Symlink("../config.json", filepath.Join(root, "skills", "rel"))
	os.Symlink(filepath.Join(root, "tools", "pkg", "skill"), filepath.Join(root, "skills", "abs-inside"))
	os.Symlink(filepath.Join(outside, "skill"), filepath.Join(root, "skills", "program"))
	links, size, err := ScanLinks(root, []string{outside})
	if err != nil || len(links) != 2 {
		t.Fatalf("%+v %v", links, err)
	}
	if links[0].Rel != "skills/abs-inside" || links[0].Class != action.LinkInside || !links[0].Dir {
		t.Fatalf("inside: %+v", links[0])
	}
	if links[1].Rel != "skills/program" || links[1].Class != action.LinkBody || links[1].Digest == "" || size != int64(len("program")) {
		t.Fatalf("body: %+v size %d", links[1], size)
	}
	// Without a recorded program location the outside link is refused.
	if _, _, err := ScanLinks(root, nil); err == nil || !strings.Contains(err.Error(), "来源无法核实") {
		t.Fatalf("no bodies: %v", err)
	}
	// The plan's links make the tree acceptable; without them it is not.
	if _, _, err := TreeFacts(root); err == nil {
		t.Fatal("absolute links accepted without a plan")
	}
	if _, _, err := TreeFactsAllow(root, OriginalTexts(links)); err != nil {
		t.Fatal(err)
	}
}

func TestScanLinksRefusesOutsideAndDangling(t *testing.T) {
	root, outside := linkWorld(t)
	other := filepath.Join(filepath.Dir(root), "elsewhere")
	os.MkdirAll(other, 0o755)
	os.Symlink(other, filepath.Join(root, "skills", "x"))
	if _, _, err := ScanLinks(root, []string{outside}); err == nil || !strings.Contains(err.Error(), "skills/x") {
		t.Fatalf("outside: %v", err)
	}
	os.Remove(filepath.Join(root, "skills", "x"))
	os.Symlink(filepath.Join(root, "missing"), filepath.Join(root, "skills", "gone"))
	if _, _, err := ScanLinks(root, []string{outside}); err == nil || !strings.Contains(err.Error(), "悬空") {
		t.Fatalf("dangling: %v", err)
	}
}

func TestStageLinksRoundTrip(t *testing.T) {
	root, outside := linkWorld(t)
	os.Symlink(filepath.Join(root, "tools", "pkg", "skill"), filepath.Join(root, "skills", "abs-inside"))
	os.Symlink(filepath.Join(outside, "skill"), filepath.Join(root, "skills", "program"))
	links, _, err := ScanLinks(root, []string{outside})
	if err != nil {
		t.Fatal(err)
	}
	allow := OriginalTexts(links)
	private := filepath.Join(filepath.Dir(root), ".valise-action-x")
	os.Mkdir(private, 0o700)
	stage := filepath.Join(private, "stage")
	if _, err := CopyTree(root, stage, allow); err != nil {
		t.Fatal(err)
	}
	staged, err := StageLinks(stage, root, private, links)
	if err != nil {
		t.Fatal(err)
	}
	// While staged, both links lead into the private area, never to live.
	for _, rel := range []string{"skills/abs-inside", "skills/program"} {
		got, _ := filepath.EvalSymlinks(filepath.Join(stage, rel))
		if !strings.HasPrefix(got, private) {
			t.Fatalf("%s resolves to %s", rel, got)
		}
	}
	if err := CheckPrograms(private, links); err != nil {
		t.Fatal(err)
	}
	if err := RestoreLinks(stage, links, staged); err != nil {
		t.Fatal(err)
	}
	live, _ := TreeDigestAllow(root, allow)
	back, err := TreeDigestAllow(stage, allow)
	if err != nil || back != live {
		t.Fatalf("restored stage differs from live: %v", err)
	}
}

func TestCheckProgramsNamesChangedFile(t *testing.T) {
	root, outside := linkWorld(t)
	os.Symlink(filepath.Join(outside, "skill"), filepath.Join(root, "skills", "program"))
	links, _, _ := ScanLinks(root, []string{outside})
	private := filepath.Join(filepath.Dir(root), ".valise-action-y")
	os.Mkdir(private, 0o700)
	stage := filepath.Join(private, "stage")
	if _, err := CopyTree(root, stage, OriginalTexts(links)); err != nil {
		t.Fatal(err)
	}
	if _, err := StageLinks(stage, root, private, links); err != nil {
		t.Fatal(err)
	}
	// A configuration command writing a cache file through the link.
	os.WriteFile(filepath.Join(stage, "skills", "program", "cache.json"), []byte("x"), 0o644)
	err := CheckPrograms(private, links)
	if err == nil || !strings.Contains(err.Error(), "skills/program/cache.json") {
		t.Fatalf("change not named: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "skill", "cache.json")); !os.IsNotExist(err) {
		t.Fatal("the original program was written")
	}
}
