package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"valise/client/config"
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
	if _, err := s.Enroll(ctx, "box", EnrollOptions{ProfileID: "demo", Assign: []string{"state:notes/**=memory"}, Accept: true}); err != nil {
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

// proceedConfirm stands for a person who chose "我清楚，继续" and confirmed the swap.
func proceedConfirm() ReviveConfirm {
	return ReviveConfirm{
		UnverifiedSource: func(UnverifiedSource) SourceDecision { return SourceProceed },
		Swap:             func(*revive.StageVerifyResult) bool { return true },
	}
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
	u := rp.UnverifiedSource
	if u == nil || u.Notice != UnverifiedSourceNotice || u.InstanceID != "demo" || u.ProfileID != "demo" || u.ProfileVersion != 1 ||
		u.SourceServer != "box" || u.Period == "" || u.Tier != vault.TierSoul || u.CreatedAt.IsZero() || !u.BackupFirst || u.BackupInstance != "demo" {
		t.Fatalf("legacy snapshot source shown as verified: %+v", u)
	}
	asked := false
	res, err := s.ExecuteReviveConfirmed(ctx, rp, "", ReviveConfirm{
		UnverifiedSource: func(UnverifiedSource) SourceDecision { return SourceProceed },
		Swap:             func(v *revive.StageVerifyResult) bool { asked = v.Files > 0; return true },
	})
	if err != nil || !asked || res.State != revive.StateRestored || rp.PreRestoreBackup != nil {
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
	_, err = s.ExecuteReviveConfirmed(ctx, rp, "crash-at="+revive.StateLiveMoved, proceedConfirm())
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
	// A new copy has no current state to back up: only "我清楚，继续".
	if rp.UnverifiedSource == nil || rp.UnverifiedSource.BackupFirst {
		t.Fatalf("clone source: %+v", rp.UnverifiedSource)
	}
	_, err = s.ExecuteReviveConfirmed(ctx, rp, "", ReviveConfirm{
		UnverifiedSource: func(UnverifiedSource) SourceDecision { return SourceBackupFirst },
		Swap:             func(*revive.StageVerifyResult) bool { t.Fatal("staged without a valid choice"); return false },
	})
	wantCode(t, err, "backup_first_unavailable")
	res, err := s.ExecuteReviveConfirmed(ctx, rp, "", proceedConfirm())
	if err != nil || res.State != revive.StateRestored {
		t.Fatalf("%+v %v", res, err)
	}
	cloneRec := readFile(t, filepath.Join(home2, ".config/valise/dossiers/demo.json"))
	if readFile(t, filepath.Join(home2, ".demo/.env")) != "K=v" || !strings.Contains(cloneRec, home2) {
		t.Fatal("clone not in place or record missing")
	}
	if !strings.Contains(cloneRec, `"glob": "notes/**"`) {
		t.Fatalf("local_overrides not carried into the clone:\n%s", cloneRec)
	}
	if st, _ := os.Stat(filepath.Join(home2, ".demo/.env")); st.Mode().Perm() != 0o600 {
		t.Fatalf("secret mode %v", st.Mode())
	}

	// Migrating the original over the clone supersedes the clone.
	if _, err := s.Enroll(ctx, "box2", EnrollOptions{ProfileID: "demo", InstanceID: "demo-clone", Assign: []string{"state:notes/**=memory"}, Accept: true}); err != nil {
		t.Fatal(err)
	}
	rp, err = s.PlanRevive(ctx, ReviveOptions{Target: "box2", From: "box", SnapshotID: ss, Mode: revive.ModeMigrate, AllowDraft: true}, pass)
	if err != nil || len(rp.Blockers) != 0 || len(rp.Plan.Supersedes) != 1 || rp.Plan.Supersedes[0] != "demo-clone" ||
		!strings.Contains(strings.Join(rp.Warnings, "\n"), "demo-clone") {
		t.Fatalf("%+v %+v %v", rp.Plan, rp.Warnings, err)
	}
	mres, err := s.ExecuteReviveConfirmed(ctx, rp, "", proceedConfirm())
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
	// A copy placed inside another instance's directory (not a named-instance
	// location of the profile) is refused, not marked as superseding it.
	rp, err = s.PlanRevive(ctx, ReviveOptions{Target: "box2", From: "box", SnapshotID: ss, Mode: revive.ModeClone, InstanceID: "demo-inner",
		Roots: map[string]string{"state": filepath.Join(home2, ".demo", "inner")}, AllowDraft: true}, pass)
	if err != nil || len(rp.Plan.Supersedes) != 0 || !hasBlockerCode(rp, "root_inside_other") {
		t.Fatalf("inner clone: %+v %+v %v", rp.Plan.Supersedes, rp.Blockers, err)
	}
	removed, err := s.OldCleanup(ctx, "box2", "demo-clone")
	if err != nil || len(removed) != 2 {
		t.Fatalf("%v %v", removed, err)
	}
	if old, _ := s.OldList(ctx, "box2"); len(old.Entries) != 0 {
		t.Fatalf("%+v", old)
	}
	if readFile(t, filepath.Join(home2, ".demo/.env")) != "K=v" {
		t.Fatal("cleanup touched the live instance")
	}
}

func TestReviveWithProfileFromSnapshot(t *testing.T) {
	s, home, soul, _ := sealedWorld(t)
	ctx := context.Background()
	// The client's profile moves on to a newer version after the seal.
	p := filepath.Join(s.Dirs.Data, "profiles", "demo.yaml")
	newer := strings.Replace(readFile(t, p), "profile_version: 1", "profile_version: 2", 1)
	if newer == readFile(t, p) {
		t.Fatal("fixture profile_version not found")
	}
	os.WriteFile(p, []byte(newer), 0o644)

	o := ReviveOptions{Target: "box", SnapshotID: soul, Mode: revive.ModeRollback, AllowDraft: true}
	rp, err := s.PlanRevive(ctx, o, pass)
	if err != nil || !rp.ProfileFromSnapshot || len(rp.Blockers) != 1 || rp.Blockers[0].Code != "profile_from_snapshot" {
		t.Fatalf("%+v %v", rp, err)
	}
	o.AllowSnapshotProfile = true
	rp, err = s.PlanRevive(ctx, o, pass)
	if err != nil || len(rp.Blockers) != 0 || rp.Plan.Profile.ProfileVersion != 1 {
		t.Fatalf("%+v %v", rp.Blockers, err)
	}
	os.WriteFile(filepath.Join(home, ".demo/memory/a.md"), []byte("changed"), 0o644)
	res, err := s.ExecuteReviveConfirmed(ctx, rp, "", proceedConfirm())
	if err != nil || res.State != revive.StateRestored || readFile(t, filepath.Join(home, ".demo/memory/a.md")) != "m" {
		t.Fatalf("%+v %v", res, err)
	}
}

// A landing on a server with tiers runs on the daily key: the passphrase
// given for the plan proves the owner, stage-write runs with porter's
// one-time grant, and no admin login is needed (docs/ARCHITECTURE.md 2.2).
func TestRollbackOnDailyKey(t *testing.T) {
	s, _, home := addVPS(t, "")
	ctx := context.Background()
	for rel, body := range map[string]string{
		".demo/demo.json": "{}", ".demo/.env": "K=v", ".demo/memory/a.md": "m", ".demo/notes/x.md": "n",
		".local/bin/demo": "#!/bin/sh\necho demo 1.0\n",
	} {
		p := filepath.Join(home, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o755)
	}
	os.MkdirAll(filepath.Join(s.Dirs.Data, "profiles"), 0o755)
	// Still a draft (a pending rule), but its landing was tested: no
	// consent needed (docs/PROFILES.md 1.2).
	tested := demoProfile + "capabilities:\n  restore: { status: tested, versions: [\"1.0\"], env: \"fake host\" }\n"
	os.WriteFile(filepath.Join(s.Dirs.Data, "profiles", "demo.yaml"), []byte(tested), 0o644)
	if _, err := s.KeyInit(pass); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.KeyRegister(ctx, "vps", "test"); err != nil {
		t.Fatal(err)
	}
	s.OwnerAuth.Passphrase = func(string) (string, error) { return pass, nil }
	if _, err := s.Enroll(ctx, "vps", EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatal(err)
	}
	snap, err := s.Seal(ctx, "vps", vaultrpc.SealParams{InstanceID: "demo", Tier: vault.TierSoul})
	if err != nil {
		t.Fatal(err)
	}
	// From here any admin login fails, and the owner is only proved by
	// the plan's passphrase.
	list, _ := config.LoadServerList(s.Dirs.Data)
	list[0].Login, list[0].LoginKey = config.LoginKey, s.keyPath("vps", "nonexistent")
	config.SaveServerList(s.Dirs.Data, list)
	s.ForgetOwner()
	s.OwnerAuth.Passphrase = nil
	os.WriteFile(filepath.Join(home, ".demo/memory/a.md"), []byte("changed after seal"), 0o644)

	rp, err := s.PlanRevive(ctx, ReviveOptions{Target: "vps", SnapshotID: snap.SnapshotID, Mode: revive.ModeRollback}, pass)
	if err != nil || len(rp.Blockers) > 0 {
		t.Fatalf("%+v %v", rp.Blockers, err)
	}
	res, err := s.ExecuteReviveConfirmed(ctx, rp, "", proceedConfirm())
	if err != nil || res.State != revive.StateRestored {
		t.Fatalf("%+v %v", res, err)
	}
	if readFile(t, filepath.Join(home, ".demo/memory/a.md")) != "m" {
		t.Fatal("memory not rolled back")
	}
	// A week-old leftover may go at the owner tier; a fresh one needs
	// developer mode (here: the failing admin login).
	if _, err := s.PrevDrop(ctx, "vps", res.OpID); err == nil {
		t.Fatal("fresh leftovers dropped without developer mode")
	}
}

func hasBlockerCode(rp *RevivePlan, code string) bool {
	for _, b := range rp.Blockers {
		if b.Code == code {
			return true
		}
	}
	return false
}
