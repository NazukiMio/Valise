package environment

import (
	"strings"
	"testing"

	"valise/core/vault"
)

func fixtureCategory(_, p string) string {
	if strings.HasPrefix(p, "work/") {
		return "workspace"
	}
	return "memory"
}

func material(tier string, second int64, paths ...string) Material {
	m := Material{Source: source(tier, second), Files: vault.Files{Format: vault.FormatFiles}}
	for _, p := range paths {
		m.Files.Files = append(m.Files.Files, vault.File{Root: "state", Path: p, Category: fixtureCategory("state", p)})
	}
	return m
}

func TestComposeReplacesDeletedContentAndLabelsOrigins(t *testing.T) {
	base := material(vault.TierFull, 100, "memory/deleted", "memory/changed", "work/preserved")
	target := material(vault.TierSoul, 200, "memory/changed", "memory/new")
	plan, err := Compose(target, &base, []string{"state"}, fixtureCategory)
	if err != nil {
		t.Fatal(err)
	}
	if plan.NeedsDataFoundation || len(plan.Entries) != 3 || len(plan.Removed) != 2 {
		t.Fatal(plan)
	}
	for _, e := range plan.Entries {
		if e.Path == "memory/deleted" {
			t.Fatal("baseline resurrected deleted memory")
		}
		want := target.Source.Snapshot
		if e.Path == "work/preserved" {
			want = base.Source.Snapshot
		}
		if e.Source != want {
			t.Fatal("lost source provenance", e)
		}
	}
	plan, err = Compose(target, nil, []string{"state"}, fixtureCategory)
	if err != nil || !plan.NeedsDataFoundation || len(plan.Entries) != 2 {
		t.Fatal("missing baseline did not require recorded-source rebuild", plan, err)
	}
}

func TestComposeRejectsUnsafeAndConflictingManifests(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*Material)
	}{
		{"escape", func(m *Material) { m.Files.Files[0].Path = "../outside" }},
		{"root", func(m *Material) { m.Files.Files[0].Root = "undeclared" }},
		{"duplicate", func(m *Material) { m.Files.Files = append(m.Files.Files, m.Files.Files[0]) }},
		{"parent file", func(m *Material) {
			m.Files.Files = append(m.Files.Files, vault.File{Root: "state", Path: "memory", Category: "memory"})
		}},
		{"outside tier", func(m *Material) { m.Files.Files[0].Path, m.Files.Files[0].Category = "work/hidden", "workspace" }},
		{"category mismatch", func(m *Material) { m.Files.Files[0].Category = "workspace" }},
		{"escaping link", func(m *Material) {
			m.Files.Symlinks = []vault.Symlink{{Root: "state", Path: "link", Target: "../../host"}}
		}},
		{"conflicting link", func(m *Material) {
			m.Files.Symlinks = []vault.Symlink{{Root: "state", Path: "memory", Target: "somewhere"}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := material(vault.TierSoul, 200, "memory/item")
			tc.edit(&m)
			if _, err := Compose(m, nil, []string{"state"}, fixtureCategory); err != ErrManifest {
				t.Fatal("accepted unsafe manifest", err)
			}
		})
	}
	base := material(vault.TierFull, 201, "work/file")
	if _, err := Compose(material(vault.TierSoul, 200), &base, []string{"state"}, fixtureCategory); err == nil {
		t.Fatal("accepted future baseline")
	}
}
