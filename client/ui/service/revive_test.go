package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"valise/core/revive"
	"valise/core/vault"
	"valise/core/vaultrpc"
)

// sealedWorld enrolls the demo fixture, registers the key and seals soul and
// soul-secrets. The demo profile has no service unit (foreground harness).
func sealedWorld(t *testing.T) (*Service, string, string, string) {
	t.Helper()
	s, home := enrollWorld(t)
	ctx := context.Background()
	if _, err := s.KeyInit(pass); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enroll(ctx, "box", EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.KeyRegister(ctx, "box", "test"); err != nil {
		t.Fatal(err)
	}
	soul, err := s.Seal(ctx, "box", vaultrpc.SealParams{InstanceID: "demo", Tier: vault.TierSoul})
	if err != nil {
		t.Fatal(err)
	}
	ss, err := s.Seal(ctx, "box", vaultrpc.SealParams{InstanceID: "demo", Tier: vault.TierSoulSecrets})
	if err != nil {
		t.Fatal(err)
	}
	return s, home, soul.SnapshotID, ss.SnapshotID
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRollbackOverFakeSSH(t *testing.T) {
	s, home, soul, _ := sealedWorld(t)
	ctx := context.Background()
	state := filepath.Join(home, ".demo")
	os.WriteFile(filepath.Join(state, "memory/a.md"), []byte("changed after seal"), 0o644)
	os.WriteFile(filepath.Join(state, ".env"), []byte("K=rotated"), 0o600)

	o := ReviveOptions{Target: "box", SnapshotID: soul, Mode: revive.ModeRollback}
	rp, err := s.PlanRevive(ctx, o, pass)
	if err != nil {
		t.Fatal(err)
	}
	if len(rp.Blockers) != 1 || rp.Blockers[0].Code != "profile_draft" {
		t.Fatalf("draft profile must block: %+v", rp.Blockers)
	}
	o.AllowDraft = true
	rp, err = s.PlanRevive(ctx, o, pass)
	if err != nil || len(rp.Blockers) != 0 || rp.Plan.Unit != "" || !rp.Plan.Carry {
		t.Fatalf("%+v %v", rp, err)
	}
	asked := false
	res, err := s.ExecuteRevive(ctx, rp, "", func(v *revive.StageVerifyResult) bool { asked = v.Files > 0; return true })
	if err != nil || !asked || res.State != revive.StateRestored {
		t.Fatalf("%+v %v", res, err)
	}
	if readFile(t, filepath.Join(state, "memory/a.md")) != "m" {
		t.Fatal("memory not rolled back")
	}
	if readFile(t, filepath.Join(state, ".env")) != "K=rotated" {
		t.Fatal("current secrets must be kept by a soul rollback")
	}
	prev, err := s.PrevList(ctx, "box")
	if err != nil || len(prev.Entries) != 1 {
		t.Fatalf("%+v %v", prev, err)
	}
	if removed, err := s.PrevDrop(ctx, "box", res.OpID); err != nil || len(removed) != 1 {
		t.Fatalf("%v %v", removed, err)
	}
}

func TestCrashInjectionAndResume(t *testing.T) {
	s, home, soul, _ := sealedWorld(t)
	ctx := context.Background()
	state := filepath.Join(home, ".demo")
	os.WriteFile(filepath.Join(state, "memory/a.md"), []byte("live"), 0o644)
	rp, err := s.PlanRevive(ctx, ReviveOptions{Target: "box", SnapshotID: soul, Mode: revive.ModeRollback, AllowDraft: true}, pass)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.ExecuteRevive(ctx, rp, "crash-at="+revive.StateLiveMoved, func(*revive.StageVerifyResult) bool { return true })
	if err == nil {
		t.Fatal("injected crash did not surface")
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatal("expected the live dir to be moved away at LIVE_MOVED")
	}
	doc, err := s.Doctor(ctx, "box")
	if err != nil || len(doc.PendingOps) != 1 || doc.PendingOps[0].State != revive.StateLiveMoved {
		t.Fatalf("%+v %v", doc, err)
	}
	res, err := s.ReviveResume(ctx, "box", rp.Plan.OpID, "back", "")
	if err != nil || res.State != revive.StateRolledBack {
		t.Fatalf("%+v %v", res, err)
	}
	if readFile(t, filepath.Join(state, "memory/a.md")) != "live" {
		t.Fatal("back did not restore the pre-swap state")
	}
}

func TestCloneToAnotherServer(t *testing.T) {
	s, _, _, ss := sealedWorld(t)
	ctx := context.Background()
	// A second fake server with its own porter.
	root := os.Getenv("FAKESSH_ROOT")
	home2 := filepath.Join(root, "hosts", "box2", "home")
	os.MkdirAll(filepath.Join(home2, ".local/bin"), 0o755)
	porterBin, _ := os.ReadFile(filepath.Join(root, "hosts", "box", "home", ".local/bin/valise-porter"))
	os.WriteFile(filepath.Join(home2, ".local/bin/valise-porter"), porterBin, 0o755)
	os.WriteFile(filepath.Join(home2, ".local/bin/demo"), []byte("#!/bin/sh\necho demo 1.0\n"), 0o755)

	rp, err := s.PlanRevive(ctx, ReviveOptions{Target: "box2", From: "box", SnapshotID: ss, Mode: revive.ModeClone, AllowDraft: true}, pass)
	if err != nil || len(rp.Blockers) != 0 {
		t.Fatalf("%+v %v", rp, err)
	}
	if rp.Plan.InstanceID != "demo-clone" || rp.Plan.Carry || rp.Plan.Roots[0].Path != filepath.Join(home2, ".demo") {
		t.Fatalf("%+v", rp.Plan)
	}
	res, err := s.ExecuteRevive(ctx, rp, "", func(*revive.StageVerifyResult) bool { return true })
	if err != nil || res.State != revive.StateRestored {
		t.Fatalf("%+v %v", res, err)
	}
	if readFile(t, filepath.Join(home2, ".demo/.env")) != "K=v" || !strings.Contains(readFile(t, filepath.Join(home2, ".config/valise/instances/demo-clone.json")), home2) {
		t.Fatal("clone not in place or record missing")
	}
	if st, _ := os.Stat(filepath.Join(home2, ".demo/.env")); st.Mode().Perm() != 0o600 {
		t.Fatalf("secret mode %v", st.Mode())
	}

	// Migrating the original over the clone supersedes the clone.
	rp, err = s.PlanRevive(ctx, ReviveOptions{Target: "box2", From: "box", SnapshotID: ss, Mode: revive.ModeMigrate, AllowDraft: true}, pass)
	if err != nil || len(rp.Blockers) != 0 || len(rp.Plan.Supersedes) != 1 || rp.Plan.Supersedes[0] != "demo-clone" ||
		!strings.Contains(strings.Join(rp.Warnings, "\n"), "demo-clone") {
		t.Fatalf("%+v %+v %v", rp.Plan, rp.Warnings, err)
	}
	mres, err := s.ExecuteRevive(ctx, rp, "", func(*revive.StageVerifyResult) bool { return true })
	if err != nil || mres.State != revive.StateRestored {
		t.Fatalf("%+v %v", mres, err)
	}
	old, err := s.OldList(ctx, "box2")
	if err != nil || len(old.Entries) != 1 || old.Entries[0].InstanceID != "demo-clone" || old.Entries[0].Superseded.By != "demo" ||
		len(old.Entries[0].Leftovers) != 1 {
		t.Fatalf("%+v %v", old, err)
	}
	if _, err := s.Seal(ctx, "box2", vaultrpc.SealParams{InstanceID: "demo-clone", Tier: vault.TierSoul}); err == nil {
		t.Fatal("sealed a superseded instance")
	}
	removed, err := s.OldCleanup(ctx, "box2", "demo-clone")
	if err != nil || len(removed) != 3 {
		t.Fatalf("%v %v", removed, err)
	}
	if old, _ := s.OldList(ctx, "box2"); len(old.Entries) != 0 {
		t.Fatalf("%+v", old)
	}
	if readFile(t, filepath.Join(home2, ".demo/.env")) != "K=v" {
		t.Fatal("cleanup touched the live instance")
	}
}
