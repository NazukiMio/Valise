package kit

import (
	"crypto/sha256"
	"encoding/hex"
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

func TestStatusAndUninstall(t *testing.T) {
	home := t.TempDir()
	ws := filepath.Join(home, "ws")
	p := profile.Profile{ID: "demo", ProfileVersion: 1, Roots: []profile.Root{{ID: "workspace"}},
		AgentKit: &profile.AgentKit{SkillsDir: &profile.RootPath{Root: "workspace", Path: "skills/"}}}
	rec := enroll.Record{InstanceID: "demo", ProfileID: "demo", ProfileVersion: 1, Roots: map[string]string{"state": home, "workspace": ws}}
	porterenroll.WriteRecord(home, rec, p)
	shipped := []agentkit.SkillFile{
		{Skill: "valise-upkeep", Name: "SKILL.md", Content: []byte("upkeep v2")},
		{Skill: "valise-enroll", Name: "SKILL.md", Content: []byte("enroll v2")},
		{Skill: "valise-checkup", Name: "SKILL.md", Content: []byte("checkup v2")},
	}
	now := time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)
	Install(home, agentkit.KitInstallParams{InstanceID: "demo", Files: shipped[:2]}, now)
	// The user edits one skill; an older install left a backup.
	os.WriteFile(filepath.Join(ws, "skills/valise-enroll/SKILL.md"), []byte("enroll, edited by the user"), 0o644)
	os.WriteFile(filepath.Join(ws, "skills/valise-upkeep/SKILL.md.valise-bak-20261030T000000Z"), []byte("upkeep v1"), 0o644)

	st, err := Status(home, agentkit.KitStatusParams{InstanceID: "demo", Files: shipped})
	if err != nil || len(st.Skills) != 3 {
		t.Fatalf("%+v %v", st, err)
	}
	want := map[string]string{"valise-upkeep": agentkit.SkillCurrent, "valise-enroll": agentkit.SkillModified, "valise-checkup": agentkit.SkillMissing}
	for _, s := range st.Skills {
		if s.State != want[s.Skill] {
			t.Errorf("%s: %s", s.Skill, s.State)
		}
		if s.Skill == "valise-upkeep" && s.Backups != 1 {
			t.Errorf("backups %d", s.Backups)
		}
	}

	dry, _ := Uninstall(home, agentkit.KitUninstallParams{InstanceID: "demo", Files: shipped, DryRun: true})
	if len(dry.Removed) != 2 || len(dry.Kept) != 1 {
		t.Fatalf("%+v", dry)
	}
	if _, err := os.Stat(filepath.Join(ws, "skills/valise-upkeep/SKILL.md")); err != nil {
		t.Fatal("dry run removed a file")
	}
	res, err := Uninstall(home, agentkit.KitUninstallParams{InstanceID: "demo", Files: shipped})
	if err != nil || len(res.Removed) != 2 || len(res.Kept) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(ws, "skills/valise-upkeep")); !os.IsNotExist(err) {
		t.Fatal("empty skill directory left behind")
	}
	if b, _ := os.ReadFile(filepath.Join(ws, "skills/valise-enroll/SKILL.md")); string(b) != "enroll, edited by the user" {
		t.Fatal("modified skill removed without --force")
	}
	res, _ = Uninstall(home, agentkit.KitUninstallParams{InstanceID: "demo", Files: shipped, Force: true})
	if len(res.Removed) != 1 {
		t.Fatalf("%+v", res)
	}
	if _, err := os.Stat(filepath.Join(ws, "skills")); err != nil {
		t.Fatal("the skills directory itself must stay")
	}
}

func TestInstallAndUninstallCleanLegacySkills(t *testing.T) {
	home := t.TempDir()
	ws := filepath.Join(home, "ws")
	p := profile.Profile{ID: "demo", ProfileVersion: 1, Roots: []profile.Root{{ID: "workspace"}},
		AgentKit: &profile.AgentKit{SkillsDir: &profile.RootPath{Root: "workspace", Path: "skills/"}}}
	rec := enroll.Record{InstanceID: "demo", ProfileID: "demo", ProfileVersion: 1, Roots: map[string]string{"state": home, "workspace": ws}}
	if _, err := porterenroll.WriteRecord(home, rec, p); err != nil {
		t.Fatal(err)
	}
	sdir := filepath.Join(ws, "skills")
	os.MkdirAll(filepath.Join(sdir, "valise-handoff"), 0o755)
	unmodifiedContent := []byte("synthetic-legacy-handoff-content")
	sum := sha256.Sum256(unmodifiedContent)
	sumHex := hex.EncodeToString(sum[:])
	legacySkillDigests["valise-handoff"][sumHex] = true
	t.Cleanup(func() { delete(legacySkillDigests["valise-handoff"], sumHex) })
	os.WriteFile(filepath.Join(sdir, "valise-handoff/SKILL.md"), unmodifiedContent, 0o644)
	os.WriteFile(filepath.Join(sdir, "valise-handoff/SKILL.md.valise-bak-20261101T000000Z"), []byte("bak"), 0o644)
	os.MkdirAll(filepath.Join(sdir, "valise-enroll"), 0o755)
	os.WriteFile(filepath.Join(sdir, "valise-enroll/SKILL.md"), []byte("user customized enroll"), 0o644)

	newKit := []agentkit.SkillFile{
		{Skill: "valise-onboard", Name: "SKILL.md", Content: []byte("onboard v1")},
		{Skill: "valise-backup", Name: "SKILL.md", Content: []byte("backup v1")},
	}
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	ins, err := Install(home, agentkit.KitInstallParams{InstanceID: "demo", Files: newKit}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(ins.Written) != 2 || len(ins.LegacyRemoved) != 2 || len(ins.LegacyKept) != 1 {
		t.Fatalf("unexpected legacy cleanup result: %+v", ins)
	}
	if _, err := os.Stat(filepath.Join(sdir, "valise-handoff")); !os.IsNotExist(err) {
		t.Fatal("empty legacy skill directory with only backup was not removed")
	}
	if _, err := os.Stat(filepath.Join(sdir, "valise-enroll/SKILL.md")); err != nil {
		t.Fatal("user-modified legacy skill should be kept")
	}

	rm, err := Uninstall(home, agentkit.KitUninstallParams{InstanceID: "demo", Files: newKit, Force: false})
	if err != nil || len(rm.Removed) != 2 || len(rm.Kept) != 1 {
		t.Fatalf("uninstall keep modified failed: %+v %v", rm, err)
	}
	rmForce, err := Uninstall(home, agentkit.KitUninstallParams{InstanceID: "demo", Files: newKit, Force: true})
	if err != nil || len(rmForce.Removed) != 1 || len(rmForce.Kept) != 0 {
		t.Fatalf("uninstall force failed: %+v %v", rmForce, err)
	}
}

func TestProposeAndDecideIdentityCategory(t *testing.T) {
	home := t.TempDir()
	state := filepath.Join(home, "state")
	if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "persona.md"), []byte("# Persona"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := profile.Profile{ID: "demo", ProfileVersion: 1}
	rec := enroll.Record{
		InstanceID:     "demo",
		ProfileID:      "demo",
		ProfileVersion: 1,
		Roots:          map[string]string{"state": state},
	}
	if _, err := porterenroll.WriteRecord(home, rec, p); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	lines, err := ProposeDossier(home, &rec, &p, []DossierProposalItem{
		{
			Kind:     agentkit.ProposalKindClassify,
			Root:     "state",
			Path:     "persona.md",
			Category: profile.CategoryIdentity,
			Reason:   "Core persona rules for soul-tier clone/migration",
		},
	}, now)
	if err != nil || len(lines) != 1 {
		t.Fatalf("ProposeDossier identity failed: lines=%v err=%v", lines, err)
	}
	ps, err := LoadProposals(home, "demo")
	if err != nil || len(ps) != 1 || ps[0].Rule.Category != profile.CategoryIdentity || ps[0].Demotion {
		t.Fatalf("unexpected pending proposals: %+v err=%v", ps, err)
	}
	dec, err := Decide(home, agentkit.DecideParams{InstanceID: "demo", Accept: []string{ps[0].ID}})
	if err != nil || len(dec.Accepted) != 1 || dec.Remaining != 0 {
		t.Fatalf("Decide identity failed: %+v err=%v", dec, err)
	}
	updated, err := porterenroll.ReadRecord(home, "demo")
	if err != nil || updated == nil || len(updated.LocalOverrides) != 1 {
		t.Fatalf("ReadRecord after Decide: %+v err=%v", updated, err)
	}
	if got := updated.LocalOverrides[0]; got.Root != "state" || got.Glob != "persona.md" || got.Category != profile.CategoryIdentity {
		t.Fatalf("unexpected LocalOverride: %+v", got)
	}
}
