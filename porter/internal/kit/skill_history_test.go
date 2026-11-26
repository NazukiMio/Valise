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

func TestStatusChangeKinds(t *testing.T) {
	home := t.TempDir()
	ws := filepath.Join(home, "ws")
	p := profile.Profile{ID: "demo", ProfileVersion: 1, Roots: []profile.Root{{ID: "workspace"}},
		AgentKit: &profile.AgentKit{SkillsDir: &profile.RootPath{Root: "workspace", Path: "skills/"}}}
	rec := enroll.Record{InstanceID: "demo", ProfileID: "demo", ProfileVersion: 1, Roots: map[string]string{"state": home, "workspace": ws}}
	porterenroll.WriteRecord(home, rec, p)
	now := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	v1 := []agentkit.SkillFile{{Skill: "valise-upkeep", Name: "SKILL.md", Content: []byte("v1")}}
	v2 := []agentkit.SkillFile{{Skill: "valise-upkeep", Name: "SKILL.md", Content: []byte("v2")}}
	st := func(files []agentkit.SkillFile) agentkit.SkillState {
		r, err := Status(home, agentkit.KitStatusParams{InstanceID: "demo", Files: files})
		if err != nil {
			t.Fatal(err)
		}
		return r.Skills[0]
	}
	file := filepath.Join(ws, "skills/valise-upkeep/SKILL.md")
	if s := st(v1); s.ChangeKind != agentkit.SkillChangeMissing {
		t.Fatalf("%+v", s)
	}
	if _, err := Install(home, agentkit.KitInstallParams{InstanceID: "demo", Files: v1}, now); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(manifestPath(home, "demo"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("manifest %v %v", fi, err)
	}
	if s := st(v1); s.ChangeKind != agentkit.SkillChangeCurrent || s.InstalledSHA256 == "" || s.InstalledAt == nil || s.DiskSHA256 == "" {
		t.Fatalf("%+v", s)
	}
	// Untouched v1 on disk, v2 shipped: official update; uninstall without Force removes it via manifest baseline.
	if s := st(v2); s.ChangeKind != agentkit.SkillChangeOfficialUpdate || s.DiskContent != "v1" {
		t.Fatalf("%+v", s)
	}
	if rm0, err := Uninstall(home, agentkit.KitUninstallParams{InstanceID: "demo", Files: v2, Force: false}); err != nil || len(rm0.Kept) != 0 {
		t.Fatalf("manifest baseline untouched skill should uninstall without force: %+v %v", rm0, err)
	}
	if _, err := Install(home, agentkit.KitInstallParams{InstanceID: "demo", Files: v1}, now); err != nil {
		t.Fatal(err)
	}
	// Edited, same shipped: local modification.
	os.WriteFile(file, []byte("mine"), 0o644)
	if s := st(v1); s.ChangeKind != agentkit.SkillChangeLocalModified {
		t.Fatalf("%+v", s)
	}
	// Edited and new shipped version: both changed.
	if s := st(v2); s.ChangeKind != agentkit.SkillChangeBothChanged {
		t.Fatalf("%+v", s)
	}
	// Historical official digest (untouched by user) uninstalls cleanly without Force
	// and drops its manifest entry; missing SKILL.md also prunes stale manifest entry.
	histContent := []byte("hist-v0")
	histSHA := sha256Hex(histContent)
	officialActiveSkillDigests["valise-upkeep"] = map[string]bool{histSHA: true}
	t.Cleanup(func() { delete(officialActiveSkillDigests, "valise-upkeep") })
	if err := os.WriteFile(file, histContent, 0o644); err != nil {
		t.Fatal(err)
	}
	rm, err := Uninstall(home, agentkit.KitUninstallParams{InstanceID: "demo", Files: v2, Force: false})
	if err != nil {
		t.Fatal(err)
	}
	if len(rm.Kept) != 0 {
		t.Fatalf("historical official digest should be removable without force, kept=%+v", rm.Kept)
	}
	if m := loadManifest(home, "demo"); len(m.Skills) != 0 {
		t.Fatalf("expected manifest entries pruned after uninstall, got %+v", m)
	}
	// Missing SKILL.md on disk still prunes stale manifest entry during uninstall.
	if _, err := Install(home, agentkit.KitInstallParams{InstanceID: "demo", Files: v1}, now); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if _, err := Uninstall(home, agentkit.KitUninstallParams{InstanceID: "demo", Files: v2, Force: false}); err != nil {
		t.Fatal(err)
	}
	if m := loadManifest(home, "demo"); len(m.Skills) != 0 {
		t.Fatalf("expected stale manifest entry pruned when file missing, got %+v", m)
	}
	// Re-install v1 to check Force uninstall still drops baseline.
	if _, err := Install(home, agentkit.KitInstallParams{InstanceID: "demo", Files: v1}, now); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Uninstall --force drops the baseline.
	if _, err := Uninstall(home, agentkit.KitUninstallParams{InstanceID: "demo", Files: v1, Force: true}); err != nil {
		t.Fatal(err)
	}
	if m := loadManifest(home, "demo"); len(m.Skills) != 0 {
		t.Fatalf("%+v", m)
	}
}

func TestClassifySkillHistoricalDigest(t *testing.T) {
	old := "679662ebc27dbeb207653e1e8fc969ba8602acea8af45f45075054447b3e467f"
	if k := classifySkill("valise-onboard", old, "zzz", skillInstall{}); k != agentkit.SkillChangeOfficialUpdate {
		t.Fatal(k)
	}
	if k := classifySkill("valise-backup", old, "zzz", skillInstall{}); k != agentkit.SkillChangeLocalModified {
		t.Fatal(k)
	}
}
