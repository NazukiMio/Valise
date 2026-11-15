package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"valise/client/config"
	"valise/core/identity"
	"valise/core/revive"
)

func TestRestoreTestUsesOnlyFilePlanAndKeepsActiveDirectory(t *testing.T) {
	s, home, _, snapshot := sealedWorld(t)
	s.OwnerAuth.Passphrase = func(string) (string, error) { return pass, nil }
	file := filepath.Join(s.Dirs.Data, "profiles/demo.yaml")
	if err := os.WriteFile(file, []byte(demoProfile+"\nservice:\n  candidates: [{scope: user, unit: demo.service}]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	control := `#!/bin/sh
echo "$*" >> "$HOME/control-calls"
case "$*" in
*is-active*) echo active ;;
*show*) echo loaded ;;
*is-enabled*) echo enabled ;;
esac
`
	if err := os.WriteFile(filepath.Join(home, ".local/bin/systemctl"), []byte(control), 0700); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(home, ".demo")
	before, err := os.Stat(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "memory/a.md"), []byte("live state"), 0600); err != nil {
		t.Fatal(err)
	}
	res, err := s.RunRestoreTest(context.Background(), "box", "demo", snapshot)
	if err != nil || !res.OK || res.State != "passed" || !res.LocalVerified || !res.ServerVerified || res.Cleanup != "removed" {
		t.Fatalf("result %+v %v", res, err)
	}
	after, _ := os.Stat(state)
	if !os.SameFile(before, after) || readFile(t, filepath.Join(state, "memory/a.md")) != "live state" {
		t.Fatal("test touched active directory")
	}
	o, err := s.ownerPorter(context.Background(), "box")
	if err != nil {
		t.Fatal(err)
	}
	var journal revive.Journal
	if err := s.callOwner(context.Background(), o, nil, revive.MethodJournal, revive.OpParams{OpID: res.OpID}, &journal); err != nil {
		t.Fatal(err)
	}
	if journal.Plan.Unit != "" || journal.Plan.InstallService || journal.Plan.Body.Action != revive.BodyKeep || journal.Plan.Carry || journal.Plan.Identity != nil {
		t.Fatalf("test retained executable restore steps: %+v", journal.Plan)
	}
	for _, h := range journal.History {
		if h.State != revive.StatePlanned && h.State != revive.StateRolledBack {
			t.Fatalf("test entered swap: %+v", h)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(home, "control-calls")); strings.Contains(string(b), " stop ") || strings.Contains(string(b), " start ") {
		t.Fatalf("test controlled active service: %s", b)
	}
}

func TestRestoreTestCancellationAndCleanupRetry(t *testing.T) {
	for _, failCleanup := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "cleanup_failure"}[failCleanup], func(t *testing.T) {
			s, home, _, snapshot := sealedWorld(t)
			s.OwnerAuth.Passphrase = func(string) (string, error) { return pass, nil }
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			interrupted := false
			ctx = WithProgress(ctx, func(p Progress) {
				if !interrupted && p.Stage == StageStage {
					interrupted = true
					cancel()
					if failCleanup {
						t.Setenv("FAKESSH_FAIL_CONNECT", "box")
					}
				}
			})
			result, err := s.RunRestoreTest(ctx, "box", "demo", snapshot)
			if err != nil || result == nil || result.OK || !interrupted {
				t.Fatalf("cancel result %+v %v", result, err)
			}
			if failCleanup {
				if result.State != "needs_cleanup" || result.Cleanup != "pending" {
					t.Fatalf("cleanup failure hidden: %+v", result)
				}
				t.Setenv("FAKESSH_FAIL_CONNECT", "")
				if _, err := s.RunRestoreTest(context.Background(), "box", "demo", snapshot); journeyCode(err) != "restore_test_pending" {
					t.Fatalf("unreconciled test replayed: %v", err)
				}
				result, err = s.CleanupRestoreTest(context.Background(), "box", "demo", result.OpID)
				if err != nil {
					t.Fatal(err)
				}
			}
			if result.State != "cancelled" || result.Cleanup != "removed" || result.OK {
				t.Fatalf("cancel claimed pass: %+v", result)
			}
			untouched(t, s, home, "m")
		})
	}
}

func TestRestoreTestCleanupRefusesAnAdvancedJournal(t *testing.T) {
	s, _, _, snapshot := sealedWorld(t)
	s.OwnerAuth.Passphrase = func(string) (string, error) { return pass, nil }
	o := ReviveOptions{Target: "box", SnapshotID: snapshot, Mode: revive.ModeRollback, AllowDraft: true, testOnly: true}
	if err := s.currentRestoreRoots(context.Background(), &o, "demo"); err != nil {
		t.Fatal(err)
	}
	p, err := s.planRevive(context.Background(), o, "")
	if err != nil {
		t.Fatal(err)
	}
	var begun struct {
		Grant string `json:"grant"`
	}
	if err := s.callOwner(context.Background(), p.target, nil, revive.MethodStageBegin, revive.StageBeginParams{Plan: p.Plan}, &begun); err != nil {
		t.Fatal(err)
	}
	trust, err := s.restoreTrust("box")
	if err != nil {
		t.Fatal(err)
	}
	record := RestoreTest{State: "running", OpID: p.Plan.OpID, Snapshot: snapshot, TrustDigest: trust, Cleanup: "pending", At: time.Now()}
	if err := s.recordRestoreTest("box", "demo", &record); err != nil {
		t.Fatal(err)
	}
	// Test fixture simulates another operation advancing this journal.
	var j revive.Journal
	if err := s.callOwner(context.Background(), p.target, nil, revive.MethodJournal, revive.OpParams{OpID: record.OpID}, &j); err != nil {
		t.Fatal(err)
	}
	j.State = revive.StateStopped
	b, _ := json.Marshal(j)
	file := filepath.Join(os.Getenv("FAKESSH_ROOT"), "hosts/box/home/.local/state/valise/journal", record.OpID+".json")
	if err := os.WriteFile(file, b, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CleanupRestoreTest(context.Background(), "box", "demo", record.OpID); journeyCode(err) != "restore_test_changed" {
		t.Fatalf("advanced journal treated as test cleanup: %v", err)
	}
	for _, root := range j.Roots {
		if _, err := os.Stat(root.Stage); err != nil {
			t.Fatal("refused cleanup removed stage", err)
		}
	}
}

func TestRestoreTestHealthEvidenceDoesNotCrossGenerations(t *testing.T) {
	s := &Service{Dirs: config.Dirs{Data: t.TempDir()}}
	old := identity.AgentRef{ServerUID: "box", InstallID: "demo", InstanceID: "demo", Generation: "old"}
	current := old
	current.Generation = "new"
	record := RestoreTest{At: time.Now(), OK: true, State: "passed", AgentRef: &old}
	if err := s.recordRestoreTest("box", "demo", &record); err != nil {
		t.Fatal(err)
	}
	if item := s.restoreTestItem("box", "demo", time.Now(), &current); item.Level == HealthOK {
		t.Fatal("old generation's test became current success")
	}
	record.AgentRef = nil
	if err := s.recordRestoreTest("box", "demo", &record); err != nil {
		t.Fatal(err)
	}
	if item := s.restoreTestItem("box", "demo", time.Now(), &current); item.Level == HealthOK {
		t.Fatal("legacy unbound test became current success")
	}
}

func TestRestoreTestReconcilesLocalTemporaryFilesAfterInterruption(t *testing.T) {
	s, home, _, snapshot := sealedWorld(t)
	s.OwnerAuth.Passphrase = func(string) (string, error) { return pass, nil }
	op := revive.NewOpID(time.Now())
	dir, err := s.localRestoreTestDir(op)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "000000"), []byte("private test fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	trust, err := s.restoreTrust("box")
	if err != nil {
		t.Fatal(err)
	}
	record := RestoreTest{State: "running", Cleanup: "pending", OpID: op, Snapshot: snapshot, TrustDigest: trust, At: time.Now()}
	if err := s.recordRestoreTest("box", "demo", &record); err != nil {
		t.Fatal(err)
	}
	result, err := s.CleanupRestoreTest(context.Background(), "box", "demo", op)
	if err != nil || result.State != "cancelled" || result.Cleanup != "removed" || result.OK {
		t.Fatalf("interruption cleanup %+v %v", result, err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("local decrypted temporary files retained", err)
	}
	untouched(t, s, home, "m")
}
