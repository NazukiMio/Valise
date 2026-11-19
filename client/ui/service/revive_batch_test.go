package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"valise/core/revive"
	"valise/core/valise"
	"valise/core/vault"
	"valise/core/vaultrpc"
)

func seedDemoFiles(t *testing.T, home string) {
	t.Helper()
	for rel, body := range map[string]string{
		".demo/demo.json":   "{}",
		".demo/.env":        "K=v",
		".demo/memory/a.md": "m",
		".demo/notes/x.md":  "n",
		".local/bin/demo":   "#!/bin/sh\necho demo 1.0\n",
	} {
		p := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func backdateDoneJournal(t *testing.T, home, op string, age time.Duration) {
	t.Helper()
	p := filepath.Join(home, ".local", "state", "valise", "journal", "done", op+".json")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var j revive.Journal
	if err := json.Unmarshal(b, &j); err != nil {
		t.Fatal(err)
	}
	if len(j.History) == 0 {
		t.Fatalf("journal %s has empty history", op)
	}
	j.History[len(j.History)-1].At = time.Now().Add(-age)
	nb, err := json.Marshal(&j)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, nb, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCleanupBatchPINPromptsOnceAndClears(t *testing.T) {
	s, e, homeVPS := addVPS(t, pass)
	ctx := context.Background()
	if _, err := s.KeyInit(pass); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(s.Dirs.Data, "profiles"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Dirs.Data, "profiles", "demo.yaml"), []byte(demoProfile), 0o644); err != nil {
		t.Fatal(err)
	}

	// Source host with enrolled demo + sealed snapshot to migrate onto vps.
	homeSrc := e.AddHost("src")
	e.InstallPorter("src")
	seedDemoFiles(t, homeSrc)
	if _, err := s.Enroll(ctx, "src", EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.KeyRegister(ctx, "src", "test"); err != nil {
		t.Fatal(err)
	}
	ss, err := s.Seal(ctx, "src", vaultrpc.SealParams{InstanceID: "demo", Tier: vault.TierSoulSecrets})
	if err != nil {
		t.Fatal(err)
	}

	// Prepare vps with demo binary, clone -> demo-clone, enroll demo-clone, then migrate -> supersedes demo-clone.
	seedDemoFiles(t, homeVPS)
	os.RemoveAll(filepath.Join(homeVPS, ".demo"))
	if _, _, err := s.KeyRegister(ctx, "vps", "test"); err != nil {
		t.Fatal(err)
	}
	rpClone, err := s.PlanRevive(ctx, ReviveOptions{Target: "vps", From: "src", SnapshotID: ss.SnapshotID, Mode: revive.ModeClone, AllowDraft: true}, pass)
	if err != nil || len(rpClone.Blockers) != 0 {
		t.Fatalf("clone plan: %+v %v", rpClone, err)
	}
	if _, err := s.ExecuteReviveConfirmed(ctx, rpClone, "", proceedConfirm()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enroll(ctx, "vps", EnrollOptions{ProfileID: "demo", InstanceID: "demo-clone", Accept: true}); err != nil {
		t.Fatal(err)
	}
	rpMig, err := s.PlanRevive(ctx, ReviveOptions{Target: "vps", From: "src", SnapshotID: ss.SnapshotID, Mode: revive.ModeMigrate, AllowDraft: true}, pass)
	if err != nil || len(rpMig.Blockers) != 0 {
		t.Fatalf("migrate plan: %+v %v", rpMig, err)
	}
	mres, err := s.ExecuteReviveConfirmed(ctx, rpMig, "", proceedConfirm())
	if err != nil {
		t.Fatal(err)
	}
	ssVPS, err := s.Seal(ctx, "vps", vaultrpc.SealParams{InstanceID: "demo", Tier: vault.TierSoul})
	if err != nil {
		t.Fatal(err)
	}

	// Create two additional rollback ops on vps: one >= 7d (TierOwner) and one < 7d (TierAdmin).
	os.WriteFile(filepath.Join(homeVPS, ".demo/memory/a.md"), []byte("changed1"), 0o644)
	rp1, err := s.PlanRevive(ctx, ReviveOptions{Target: "vps", SnapshotID: ssVPS.SnapshotID, Mode: revive.ModeRollback, AllowDraft: true}, pass)
	if err != nil {
		t.Fatal(err)
	}
	res1, err := s.ExecuteReviveConfirmed(ctx, rp1, "", proceedConfirm())
	if err != nil {
		t.Fatal(err)
	}
	backdateDoneJournal(t, homeVPS, res1.OpID, 10*24*time.Hour)

	os.WriteFile(filepath.Join(homeVPS, ".demo/memory/a.md"), []byte("changed2"), 0o644)
	rp2, err := s.PlanRevive(ctx, ReviveOptions{Target: "vps", SnapshotID: ssVPS.SnapshotID, Mode: revive.ModeRollback, AllowDraft: true}, pass)
	if err != nil {
		t.Fatal(err)
	}
	res2, err := s.ExecuteReviveConfirmed(ctx, rp2, "", proceedConfirm())
	if err != nil {
		t.Fatal(err)
	}

	if err := s.EnablePIN(pass, "012345"); err != nil {
		t.Fatal(err)
	}
	s.ForgetOwner()
	s.LockAdmin()

	pinAsked, passAsked, adminAsked := 0, 0, 0
	s.OwnerAuth.PIN = func(string) (string, error) { pinAsked++; return "012345", nil }
	s.OwnerAuth.Passphrase = func(string) (string, error) { passAsked++; return pass, nil }
	s.AdminAuth.Passphrase = func(string) (string, error) { adminAsked++; return pass, nil }

	// Include res1 (>=7d TierOwner), res2 (<7d TierAdmin), overlapping mres.OpID, and demo-clone (TierAdmin).
	batchRes, err := s.CleanupBatch(ctx, "vps", CleanupBatchRequest{
		PrevOps:      []string{res1.OpID, res2.OpID, mres.OpID},
		OldInstances: []string{"demo-clone"},
	})
	if err != nil {
		t.Fatalf("CleanupBatch failed: %v", err)
	}
	if pinAsked != 1 || passAsked != 0 || adminAsked != 0 {
		t.Fatalf("prompt counts pin=%d pass=%d admin=%d, want 1/0/0", pinAsked, passAsked, adminAsked)
	}
	if batchRes.OldDone != 1 || batchRes.PrevDone != 3 || len(batchRes.Failures) != 0 || len(batchRes.Removed) == 0 {
		t.Fatalf("unexpected batch result: %+v", batchRes)
	}
	if s.hasCachedPassphrase() {
		t.Fatal("cached passphrase survived CleanupBatch in PIN mode")
	}
	if _, ok := s.OwnerRemaining(); ok {
		t.Fatal("PIN mode exposed OwnerRemaining after CleanupBatch")
	}
	prevAfter, err := s.PrevList(ctx, "vps")
	if err != nil || len(prevAfter.Entries) != 0 {
		t.Fatalf("remaining prev entries: %+v %v", prevAfter, err)
	}
	oldAfter, err := s.OldList(ctx, "vps")
	if err != nil || len(oldAfter.Entries) != 0 {
		t.Fatalf("remaining old entries: %+v %v", oldAfter, err)
	}
}

func TestCleanupBatchPassphraseFreshOnUntieredAndCancel(t *testing.T) {
	s, home, soul, _ := sealedWorld(t)
	ctx := context.Background()
	state := filepath.Join(home, ".demo")

	var ops []string
	for _, content := range []string{"dirty1", "dirty2"} {
		os.WriteFile(filepath.Join(state, "memory/a.md"), []byte(content), 0o644)
		rp, err := s.PlanRevive(ctx, ReviveOptions{Target: "box", SnapshotID: soul, Mode: revive.ModeRollback, AllowDraft: true}, pass)
		if err != nil {
			t.Fatal(err)
		}
		res, err := s.ExecuteReviveConfirmed(ctx, rp, "", proceedConfirm())
		if err != nil {
			t.Fatal(err)
		}
		ops = append(ops, res.OpID)
	}

	// Ensure the passphrase window is already warm.
	k, err := s.Unlock(pass)
	if err != nil {
		t.Fatal(err)
	}
	k.Lock()
	if _, ok := s.OwnerRemaining(); !ok {
		t.Fatal("expected warm passphrase window")
	}

	// Cancelling the batch prompt leaves all items untouched.
	asked := 0
	s.OwnerAuth.Passphrase = func(string) (string, error) {
		asked++
		return "", valise.New("cancelled", "cancelled", "")
	}
	_, err = s.CleanupBatch(ctx, "box", CleanupBatchRequest{PrevOps: ops})
	wantCode(t, err, "cancelled")
	if asked != 1 {
		t.Fatalf("expected 1 fresh prompt on warm window, got %d", asked)
	}
	prev, err := s.PrevList(ctx, "box")
	if err != nil || len(prev.Entries) != 2 {
		t.Fatalf("cancelled batch modified server: %+v %v", prev, err)
	}

	// Confirming prompts once fresh and removes both entries.
	s.OwnerAuth.Passphrase = func(string) (string, error) {
		asked++
		return pass, nil
	}
	batchRes, err := s.CleanupBatch(ctx, "box", CleanupBatchRequest{PrevOps: ops})
	if err != nil {
		t.Fatalf("CleanupBatch confirm failed: %v", err)
	}
	if asked != 2 || batchRes.PrevDone != 2 || len(batchRes.Failures) != 0 {
		t.Fatalf("asked=%d batch=%+v", asked, batchRes)
	}
	prev, err = s.PrevList(ctx, "box")
	if err != nil || len(prev.Entries) != 0 {
		t.Fatalf("expected empty prev list, got %+v %v", prev, err)
	}
}

func TestCleanupBatchPartialFailure(t *testing.T) {
	s, home, soul, _ := sealedWorld(t)
	ctx := context.Background()
	os.WriteFile(filepath.Join(home, ".demo/memory/a.md"), []byte("dirty"), 0o644)
	rp, err := s.PlanRevive(ctx, ReviveOptions{Target: "box", SnapshotID: soul, Mode: revive.ModeRollback, AllowDraft: true}, pass)
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.ExecuteReviveConfirmed(ctx, rp, "", proceedConfirm())
	if err != nil {
		t.Fatal(err)
	}

	s.OwnerAuth.Passphrase = func(string) (string, error) { return pass, nil }
	batchRes, err := s.CleanupBatch(ctx, "box", CleanupBatchRequest{
		PrevOps: []string{res.OpID, "op_missing"},
	})
	if err != nil {
		t.Fatalf("partial failure should return result with nil error, got %v", err)
	}
	if batchRes.PrevDone != 1 || len(batchRes.Failures) != 1 {
		t.Fatalf("batchRes = %+v", batchRes)
	}
	f := batchRes.Failures[0]
	if f.Kind != "prev" || f.Target != "op_missing" || f.Err == nil {
		t.Fatalf("unexpected failure entry: %+v", f)
	}
}
