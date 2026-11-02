package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"valise/core/vault"
)

// Renaming a server moves its folder in every repository and records the
// old name in TREE (docs/ARCHITECTURE.md section 3).
func TestRenameServer(t *testing.T) {
	s, _, soul, _ := sealedWorld(t)
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "usb")
	if _, err := s.RepoAdd("usb", dir); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PlanRename(ctx, "box", "home"); err == nil {
		t.Fatal("renamed a server without a name")
	}
	if err := s.ServerNameSet(ctx, "box", "box"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RepoSync(ctx, "box", "usb"); err != nil {
		t.Fatal(err)
	}
	plan, err := s.PlanRename(ctx, "box", "home")
	if err != nil || plan.Old != "box" || len(plan.Places) != 1 || plan.Places[0].Missing {
		t.Fatalf("%+v %v", plan, err)
	}
	if err := s.RenameServer(ctx, plan); err != nil || !plan.Places[0].Done {
		t.Fatalf("%+v %v", plan, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "box")); err == nil {
		t.Fatal("old folder left")
	}
	var d vault.TreeDesc
	b, _ := os.ReadFile(filepath.Join(dir, "home", vault.TreeFile))
	if json.Unmarshal(b, &d) != nil || d.ServerName != "home" || len(d.RenamedFrom) != 1 || d.RenamedFrom[0] != "box" {
		t.Fatalf("TREE %s", b)
	}
	st, err := s.VaultStatus(ctx, "box")
	if err != nil || st.ServerName != "home" {
		t.Fatal(st.ServerName, err)
	}
	// The repository keeps working under the new name.
	if _, err := s.RepoSync(ctx, "box", "usb"); err != nil {
		t.Fatal(err)
	}
	if rs := s.RepoStatuses("box", "home"); len(rs) != 1 || !rs[0].Has(soul) {
		t.Fatalf("%+v", rs)
	}
}
