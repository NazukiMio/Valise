package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"valise/core/revive"
)

// A copy may stay on the snapshot's own server (docs/UI.md 7.11): in
// directories the user names, under another id, with the service left
// alone. It never touches the original: a directory that is, holds or sits
// inside the original's is refused, not treated as superseding it.
func TestCloneOnTheSameServer(t *testing.T) {
	s, home, _, ss := sealedWorld(t)
	ctx := context.Background()
	state := filepath.Join(home, ".demo")
	os.WriteFile(filepath.Join(state, "memory/a.md"), []byte("live"), 0o644)
	copyDir := filepath.Join(home, ".demo-copy")
	clone := func(id string, roots map[string]string) (*RevivePlan, error) {
		return s.PlanRevive(ctx, ReviveOptions{Target: "box", From: "box", SnapshotID: ss, Mode: revive.ModeClone, InstanceID: id, Roots: roots, AllowDraft: true}, pass)
	}

	// A migration still has to go somewhere else.
	_, err := s.PlanRevive(ctx, ReviveOptions{Target: "box", From: "box", SnapshotID: ss, Mode: revive.ModeMigrate, AllowDraft: true}, pass)
	wantCode(t, err, "mode_mismatch")
	// No directory named: Valise does not guess one beside the original.
	_, err = clone("demo-copy", nil)
	wantCode(t, err, "copy_needs_root")
	// The original's id is taken.
	_, err = clone("demo", map[string]string{"state": copyDir})
	wantCode(t, err, "copy_same_id")
	// The original's directory, a directory inside it, and one that holds it.
	for dir, code := range map[string]string{state: "copy_overlaps", filepath.Join(state, "inner"): "root_inside_other", home: "copy_overlaps"} {
		rp, err := clone("demo-copy", map[string]string{"state": dir})
		if err != nil || !hasBlockerCode(rp, code) || len(rp.Plan.Supersedes) != 0 {
			t.Fatalf("%s: want blocker %s, got %+v %v", dir, code, rp.Blockers, err)
		}
	}

	// A harness that cannot move its state directory could not use the copy.
	rp, err := clone("demo-copy", map[string]string{"state": copyDir})
	if err != nil || !hasBlockerCode(rp, "copy_not_relocatable") {
		t.Fatalf("%+v %v", rp.Blockers, err)
	}
	profilePath := filepath.Join(s.Dirs.Data, "profiles", "demo.yaml")
	os.WriteFile(profilePath, []byte(readFile(t, profilePath)+"relocation: { env: DEMO_HOME }\n"), 0o644)

	rp, err = clone("demo-copy", map[string]string{"state": copyDir})
	if err != nil || len(rp.Blockers) != 0 || len(rp.Nested) != 0 {
		t.Fatalf("%+v %v", rp, err)
	}
	p := rp.Plan
	if p.InstanceID != "demo-copy" || p.Unit != "" || p.InstallService || len(p.Supersedes) != 0 || p.Carry || rp.StopSource != "" ||
		len(p.Roots) != 1 || p.Roots[0].Path != copyDir {
		t.Fatalf("plan %+v stop %q", p, rp.StopSource)
	}
	if w := strings.Join(rp.Warnings, "\n"); !strings.Contains(w, "不启动、不停止任何服务") || !strings.Contains(w, "DEMO_HOME") || strings.Contains(w, "会先停止服务") {
		t.Fatalf("the plan does not say the service is left alone: %q", rp.Warnings)
	}
	res, err := s.ExecuteReviveConfirmed(ctx, rp, "", proceedConfirm())
	if err != nil || res.State != revive.StateRestored {
		t.Fatalf("%+v %v", res, err)
	}
	// The copy is in its own directory…
	if readFile(t, filepath.Join(copyDir, "memory/a.md")) != "m" {
		t.Fatal("copy not in place")
	}
	// …and the original is as it was: its files, and still current.
	if readFile(t, filepath.Join(state, "memory/a.md")) != "live" || readFile(t, filepath.Join(state, ".env")) != "K=v" {
		t.Fatal("the original's files changed")
	}
	// Both are instances of the one installation, each with its own directory.
	dossier := readFile(t, filepath.Join(home, ".config/valise/dossiers/demo.json"))
	if !strings.Contains(dossier, copyDir) || !strings.Contains(dossier, `"`+state+`"`) || strings.Contains(dossier, "superseded") {
		t.Fatalf("dossier:\n%s", dossier)
	}
	list, err := s.Instances(ctx, "box")
	roots := map[string]string{}
	for _, in := range list {
		roots[in.InstanceID] = in.Roots["state"]
	}
	if err != nil || len(list) != 2 || roots["demo"] != state || roots["demo-copy"] != copyDir {
		t.Fatalf("instances %+v %v", roots, err)
	}
	// A second copy under the same id is refused.
	rp, err = clone("demo-copy", map[string]string{"state": filepath.Join(home, ".demo-copy2")})
	if err != nil || !hasBlockerCode(rp, "copy_id_taken") {
		t.Fatalf("%+v %v", rp.Blockers, err)
	}
}
