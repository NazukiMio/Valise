package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"valise/core/identity"
	"valise/core/protection"
	"valise/core/revive"
	"valise/core/vault"
)

// untouched checks that nothing was staged or swapped on box.
func untouched(t *testing.T, s *Service, home, memory string) {
	t.Helper()
	ctx := context.Background()
	if readFile(t, filepath.Join(home, ".demo/memory/a.md")) != memory {
		t.Fatal("live state changed")
	}
	prev, err := s.PrevList(ctx, "box")
	if err != nil || len(prev.Entries) != 0 {
		t.Fatalf("stage or swap left state: %+v %v", prev, err)
	}
	doc, err := s.Doctor(ctx, "box")
	if err != nil || len(doc.PendingOps) != 0 {
		t.Fatalf("operation begun: %+v %v", doc, err)
	}
}

func unverifiedRollback(t *testing.T) (*Service, string, *RevivePlan) {
	t.Helper()
	s, home, soul, _ := sealedWorld(t)
	os.WriteFile(filepath.Join(home, ".demo/memory/a.md"), []byte("changed"), 0o644)
	rp, err := s.PlanRevive(context.Background(), ReviveOptions{Target: "box", SnapshotID: soul, Mode: revive.ModeRollback, AllowDraft: true}, pass)
	if err != nil || len(rp.Blockers) != 0 || rp.UnverifiedSource == nil || !rp.UnverifiedSource.BackupFirst {
		t.Fatalf("%+v %v", rp, err)
	}
	return s, home, rp
}

func TestUnverifiedSourceRefusals(t *testing.T) {
	s, home, rp := unverifiedRollback(t)
	ctx := context.Background()
	never := func(*revive.StageVerifyResult) bool { t.Fatal("staged without a source decision"); return false }
	// No in-process human callback: refused, whatever else the caller has.
	_, err := s.ExecuteRevive(ctx, rp, "", never)
	wantCode(t, err, "source_unverified")
	// The decision is not a request field: the plan's JSON carries none.
	b, _ := json.Marshal(rp)
	if strings.Contains(string(b), `"decision"`) || strings.Contains(string(b), `"confirmed"`) {
		t.Fatalf("plan JSON carries a decision: %s", b)
	}
	_, err = s.ExecuteReviveConfirmed(ctx, rp, "", ReviveConfirm{UnverifiedSource: func(UnverifiedSource) SourceDecision { return SourceCancel }, Swap: never})
	wantCode(t, err, "cancelled")
	_, err = s.ExecuteReviveConfirmed(ctx, rp, "", ReviveConfirm{UnverifiedSource: func(UnverifiedSource) SourceDecision { return "confirmed" }, Swap: never})
	wantCode(t, err, "cancelled")
	untouched(t, s, home, "changed")
}

func TestUnverifiedSourceBackupFirst(t *testing.T) {
	s, home, rp := unverifiedRollback(t)
	ctx := context.Background()
	before, err := s.VaultStatus(ctx, "box")
	if err != nil {
		t.Fatal(err)
	}
	var shown UnverifiedSource
	res, err := s.ExecuteReviveConfirmed(ctx, rp, "", ReviveConfirm{
		UnverifiedSource: func(u UnverifiedSource) SourceDecision { shown = u; return SourceBackupFirst },
		Swap: func(*revive.StageVerifyResult) bool {
			// The backup exists before the swap is even offered.
			if rp.PreRestoreBackup == nil {
				t.Fatal("staged before the backup")
			}
			return true
		},
	})
	if err != nil || res.State != revive.StateRestored {
		t.Fatalf("%+v %v", res, err)
	}
	if shown.BackupInstance != "demo" || shown.Notice != UnverifiedSourceNotice {
		t.Fatalf("shown: %+v", shown)
	}
	bk := rp.PreRestoreBackup
	if bk == nil || bk.Tier != vault.TierFull || bk.InstanceID != "demo" {
		t.Fatalf("pre-restore backup: %+v", bk)
	}
	after, err := s.VaultStatus(ctx, "box")
	if err != nil || len(after.Snapshots) != len(before.Snapshots)+1 {
		t.Fatalf("backup not in vault: %+v %v", after, err)
	}
	log, err := s.SealLog(ctx, "box", "demo")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range log.Entries {
		if e.SnapshotID == bk.SnapshotID && strings.Contains(e.Reason, rp.Plan.SnapshotID) && strings.Contains(e.Reason, "先备份当前状态") {
			found = true
		}
	}
	if !found {
		t.Fatalf("backup reason not recorded: %+v", log.Entries)
	}
	if readFile(t, filepath.Join(home, ".demo/memory/a.md")) != "m" {
		t.Fatal("memory not rolled back")
	}
	// The usual pre-swap state is still kept as before.
	if prev, err := s.PrevList(ctx, "box"); err != nil || len(prev.Entries) != 1 {
		t.Fatalf("%+v %v", prev, err)
	}
}

func TestUnverifiedSourceBackupFailureStopsBeforeStaging(t *testing.T) {
	s, home, rp := unverifiedRollback(t)
	ctx := context.Background()
	_, err := s.ExecuteReviveConfirmed(ctx, rp, "", ReviveConfirm{
		UnverifiedSource: func(UnverifiedSource) SourceDecision {
			t.Setenv("FAKESSH_FAIL_CONNECT", "box") // the backup cannot reach the server
			return SourceBackupFirst
		},
		Swap: func(*revive.StageVerifyResult) bool { t.Fatal("staged after a failed backup"); return false },
	})
	wantCode(t, err, "pre_restore_backup_failed")
	if rp.PreRestoreBackup != nil {
		t.Fatal("failed backup reported")
	}
	t.Setenv("FAKESSH_FAIL_CONNECT", "")
	untouched(t, s, home, "changed")
}

// A snapshot from a planned backup has a bound source generation: the plan
// shows no unverified source and execution asks nothing extra.
func TestKnownSourceHasNoExtraStep(t *testing.T) {
	s, home := enrollWorld(t)
	ctx := context.Background()
	for name, body := range map[string]string{"systemctl": n2Systemctl, "loginctl": "#!/bin/sh\nprintf 'yes\\n'\n"} {
		if err := os.WriteFile(filepath.Join(home, ".local/bin", name), []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Enroll(ctx, "box", EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatal(err)
	}
	var b identity.Binding
	if err := s.call(ctx, "box", identity.MethodGet, identity.GetParams{InstanceID: "demo"}, &b); err != nil || b.Agent == nil {
		t.Fatalf("%+v %v", b, err)
	}
	if _, err := s.KeyInit(pass); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.KeyRegister(ctx, "box", "test"); err != nil {
		t.Fatal(err)
	}
	s.ProtectionReview = func(context.Context, string, protection.FrozenChange) (bool, error) { return true, nil }
	s.BackupReview = func(context.Context, string, BackupPlan) (bool, error) { return true, nil }
	desired := protection.NewDefault()
	desired.Schedule.DailyAt, desired.Schedule.Timezone = "12:00", "UTC"
	desired.Period = protection.Period{Unit: "month", Timezone: "UTC"}
	change, err := s.PrepareProtectionChange(ctx, "box", protection.PrepareParams{AgentRef: b.Agent.Ref, Desired: desired})
	if err != nil {
		t.Fatal(err)
	}
	if done, err := s.StartProtectionChange(ctx, change.PlanID, "known-source-plan"); err != nil || done.State != "succeeded" {
		t.Fatalf("%+v %v", done, err)
	}
	bp, err := s.PrepareBackup(ctx, "box", b.Agent.Ref)
	if err != nil {
		t.Fatal(err)
	}
	bt, err := s.StartBackup(ctx, bp.PlanID, "known-source-backup")
	if err == nil {
		bt = waitBackup(t, s, bt)
	}
	if err != nil || bt.State != "succeeded" || bt.Run == nil || bt.Run.Snapshot == nil {
		t.Fatalf("%+v %v", bt, err)
	}
	os.WriteFile(filepath.Join(home, ".demo/memory/a.md"), []byte("changed"), 0o644)
	rp, err := s.PlanRevive(ctx, ReviveOptions{Target: "box", SnapshotID: bt.Run.Snapshot.SnapshotID, Mode: revive.ModeRollback, AllowDraft: true}, pass)
	if err != nil || len(rp.Blockers) != 0 {
		t.Fatalf("%+v %v", rp, err)
	}
	if rp.UnverifiedSource != nil || rp.Plan.Identity == nil || rp.Plan.Identity.SourceAgent == nil || *rp.Plan.Identity.SourceAgent != b.Agent.Ref {
		t.Fatalf("known source shown as unverified: %+v %+v", rp.UnverifiedSource, rp.Plan.Identity)
	}
	res, err := s.ExecuteReviveConfirmed(ctx, rp, "", ReviveConfirm{
		UnverifiedSource: func(UnverifiedSource) SourceDecision { t.Fatal("asked about a verified source"); return SourceCancel },
		Swap:             func(*revive.StageVerifyResult) bool { return true },
	})
	if err != nil || res.State != revive.StateRestored || rp.PreRestoreBackup != nil {
		t.Fatalf("%+v %v", res, err)
	}
	if readFile(t, filepath.Join(home, ".demo/memory/a.md")) != "m" {
		t.Fatal("memory not restored")
	}
}
