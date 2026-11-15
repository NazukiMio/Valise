package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"valise/core/protection"
	"valise/core/revive"
)

func TestRestoreOptionalBackupWorksForVerifiedSourceWithoutChangingPlan(t *testing.T) {
	s, home, ref := acceptedPlanWorld(t)
	s.BackupReview = approveBackup
	ctx := context.Background()
	prepared, err := s.PrepareBackup(ctx, "box", ref)
	if err != nil {
		t.Fatal(err)
	}
	backup, err := s.StartBackup(ctx, prepared.PlanID, "restore-source")
	if err != nil || backup.State != "succeeded" {
		t.Fatalf("source %+v %v", backup, err)
	}
	status, err := s.VaultStatus(ctx, "box")
	if err != nil || len(status.Snapshots) != 1 {
		t.Fatal(err)
	}
	beforePlan, err := s.GetProtection(ctx, "box", protection.GetParams{AgentRef: ref})
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.PlanRevive(ctx, ReviveOptions{Target: "box", SnapshotID: status.Snapshots[0].SnapshotID, Mode: revive.ModeRollback, AllowDraft: true}, pass)
	if err != nil || p.UnverifiedSource != nil {
		t.Fatalf("source not bound: %+v %v", p, err)
	}
	if err := os.WriteFile(filepath.Join(home, ".demo/memory/a.md"), []byte("before restore"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := s.ExecuteReviveConfirmed(ctx, p, "", ReviveConfirm{BackupCurrent: true, Swap: func(*revive.StageVerifyResult) bool {
		if p.PreRestoreBackup == nil {
			t.Fatal("backup missing before stage confirmation")
		}
		return true
	}})
	if err != nil || result.State != revive.StateRestored || p.PreRestoreBackup == nil || p.PreRestoreBackup.Tier != "full" {
		t.Fatalf("restore %+v %v", result, err)
	}
	after, err := s.VaultStatus(ctx, "box")
	if err != nil || len(after.Snapshots) != len(status.Snapshots)+1 {
		t.Fatalf("extra backup count %+v %v", after, err)
	}
	afterPlan, err := s.GetProtection(ctx, "box", protection.GetParams{AgentRef: ref})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := protection.Digest(beforePlan.ServerPlan)
	b, _ := protection.Digest(afterPlan.ServerPlan)
	if a != b {
		t.Fatal("extra backup changed protection plan")
	}
}

func TestRestoreBackupChoicesDoNotDuplicateAndFailureDoesNotStage(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "deduplicate", true: "fail_closed"}[failure], func(t *testing.T) {
			s, home, p := unverifiedRollback(t)
			ctx := context.Background()
			before, _ := s.VaultStatus(ctx, "box")
			_, err := s.ExecuteReviveConfirmed(ctx, p, "", ReviveConfirm{BackupCurrent: true, UnverifiedSource: func(UnverifiedSource) SourceDecision {
				if failure {
					t.Setenv("FAKESSH_FAIL_CONNECT", "box")
				}
				return SourceBackupFirst
			}, Swap: func(*revive.StageVerifyResult) bool {
				if failure {
					t.Fatal("staged despite failed backup")
				}
				return true
			}})
			if failure {
				wantCode(t, err, "pre_restore_backup_failed")
				t.Setenv("FAKESSH_FAIL_CONNECT", "")
				untouched(t, s, home, "changed")
			} else {
				if err != nil {
					t.Fatal(err)
				}
				after, _ := s.VaultStatus(ctx, "box")
				if len(after.Snapshots) != len(before.Snapshots)+1 {
					t.Fatal("both choices created duplicate backups")
				}
			}
		})
	}
}

func TestDesktopRestoreRecordsTheHumanBackupChoice(t *testing.T) {
	s, p, _ := desktopRestorePlan(t)
	human := proceedConfirm()
	human.BackupCurrent = true
	task, err := s.RunRestore(context.Background(), p, human, nil)
	if err != nil || task.State != "succeeded" || task.PreBackupSnapshot == "" || p.Revive.PreRestoreBackup == nil || task.PreBackupSnapshot != p.Revive.PreRestoreBackup.SnapshotID {
		t.Fatalf("desktop backup choice lost: %+v %v", task, err)
	}
	stored, err := s.ListRestoreTasks()
	if err != nil || len(stored) != 1 || stored[0].PreBackupSnapshot != task.PreBackupSnapshot {
		t.Fatalf("backup evidence not retained: %+v %v", stored, err)
	}
}
