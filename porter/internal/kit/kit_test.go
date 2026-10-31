package kit

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"valise/core/agentkit"
	"valise/core/enroll"
	"valise/core/profile"
	porterenroll "valise/porter/internal/enroll"
)

func TestInstallBacksUpChangedSkills(t *testing.T) {
	home := t.TempDir()
	ws := filepath.Join(home, "ws")
	p := profile.Profile{ID: "demo", ProfileVersion: 1, Roots: []profile.Root{{ID: "workspace"}},
		AgentKit: &profile.AgentKit{SkillsDir: &profile.RootPath{Root: "workspace", Path: "skills/"}}}
	rec := enroll.Record{InstanceID: "demo", ProfileID: "demo", ProfileVersion: 1, Roots: map[string]string{"state": home, "workspace": ws}}
	if _, err := porterenroll.WriteRecord(home, rec, p); err != nil {
		t.Fatal(err)
	}
	files := []agentkit.SkillFile{{Skill: "valise-upkeep", Name: "SKILL.md", Content: []byte("v1")}}
	now := time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)
	dry, err := Install(home, agentkit.KitInstallParams{InstanceID: "demo", Files: files, DryRun: true}, now)
	if err != nil || len(dry.Written) != 1 {
		t.Fatalf("%+v %v", dry, err)
	}
	if _, err := os.Stat(dry.Written[0]); !os.IsNotExist(err) {
		t.Fatal("dry run wrote a file")
	}
	if _, err := Install(home, agentkit.KitInstallParams{InstanceID: "demo", Files: files}, now); err != nil {
		t.Fatal(err)
	}
	again, _ := Install(home, agentkit.KitInstallParams{InstanceID: "demo", Files: files}, now)
	if len(again.Written) != 0 || len(again.Same) != 1 {
		t.Fatalf("%+v", again)
	}
	files[0].Content = []byte("v2")
	upd, _ := Install(home, agentkit.KitInstallParams{InstanceID: "demo", Files: files}, now)
	if len(upd.Backups) != 1 {
		t.Fatalf("%+v", upd)
	}
	if b, _ := os.ReadFile(upd.Backups[0]); string(b) != "v1" {
		t.Fatal("backup lost the old content")
	}
	bad := []agentkit.SkillFile{{Skill: "../evil", Name: "SKILL.md", Content: []byte("x")}}
	if _, err := Install(home, agentkit.KitInstallParams{InstanceID: "demo", Files: bad}, now); err == nil {
		t.Fatal("path traversal in skill name accepted")
	}
}

func TestHandoffCreatesMissingTarget(t *testing.T) {
	root := t.TempDir()
	rec := &enroll.Record{Roots: map[string]string{"state": root}}
	p := &profile.Profile{Handoff: &profile.Handoff{Targets: []profile.HandoffTarget{{Root: "state", Glob: "memories/MEMORY.md", Mode: "append_section", MaxLines: 5}}}}
	targets, err := HandoffTargets(rec, p)
	if err != nil || len(targets) != 1 || targets[0].Exists {
		t.Fatalf("%+v %v", targets, err)
	}
	st := &HandoffState{SnapshotID: "s_x", Targets: targets}
	if _, err := CheckHandoff(rec, st); err == nil {
		t.Fatal("nothing written yet")
	}
	os.MkdirAll(filepath.Join(root, "memories"), 0o755)
	os.WriteFile(filepath.Join(root, "memories/MEMORY.md"), []byte("## Handoff 2026-10-31\n- next\n"), 0o644)
	if _, err := CheckHandoff(rec, st); err != nil {
		t.Fatal(err)
	}
}
