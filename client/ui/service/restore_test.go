package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"valise/core/environment"
	"valise/core/journey"
	"valise/core/revive"
	"valise/core/vault"
)

func desktopRestorePlan(t *testing.T) (*Service, *PreparedRestore, string) {
	t.Helper()
	s, home, id, _ := sealedWorld(t)
	if _, err := s.AddServers("box"); err != nil {
		t.Fatal(err)
	}
	s.OwnerAuth.Passphrase = func(string) (string, error) { return pass, nil }
	settings, err := s.InstanceEnvironmentSettings(context.Background(), "box", "demo")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveEnvironmentSettings(context.Background(), "box", settings, environment.Direct); err != nil {
		t.Fatal(err)
	}
	page, err := s.ListTimeline(context.Background(), JourneyListRequest{AllLocations: true, Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	var point journey.TimelinePoint
	for _, p := range page.Items {
		if p.SnapshotRef.SnapshotID == id {
			point = p
		}
	}
	if point.SnapshotRef.VaultID == "" {
		t.Fatalf("point missing %+v", page)
	}
	sources := s.RestoreSources(point)
	if len(sources) == 0 {
		t.Fatal("no configured source")
	}
	p, err := s.PrepareRestore(context.Background(), RestoreRequest{Options: ReviveOptions{From: "box", Target: "box", SnapshotID: id, Mode: revive.ModeRollback, AllowDraft: true}, Snapshot: point.SnapshotRef, Source: sources[0]})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Revive.Blockers) > 0 {
		t.Fatalf("blockers %+v", p.Revive.Blockers)
	}
	return s, p, home
}

func TestDesktopRestoreDoesNotReplayAndSeparatesFileResult(t *testing.T) {
	s, p, home := desktopRestorePlan(t)
	path := filepath.Join(home, ".demo/memory/a.md")
	os.WriteFile(path, []byte("before"), 0600)
	tsk, err := s.RunRestore(context.Background(), p, proceedConfirm(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if tsk.State != "succeeded" || tsk.SwapState != revive.StateRestored || tsk.Relation || !tsk.Verified {
		t.Fatalf("file-only result %+v", tsk)
	}
	os.WriteFile(path, []byte("after"), 0600)
	if _, err = s.RunRestore(context.Background(), p, proceedConfirm(), nil); err == nil {
		t.Fatal("replayed an accepted operation")
	}
	if readFile(t, path) != "after" {
		t.Fatal("duplicate request modified live content")
	}
	b, err := os.ReadFile(s.restoreTaskPath(tsk.OpID))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{pass, "K=", "profile_doc", "service_logs"} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("index exposed %q", secret)
		}
	}
}

func TestDesktopRestoreStopsForExpiredChangedPlanAndStage(t *testing.T) {
	for _, scenario := range []string{"expired", "profile_changed", "directory_changed", "tampered_stage", "declined"} {
		t.Run(scenario, func(t *testing.T) {
			s, p, home := desktopRestorePlan(t)
			state := filepath.Join(home, ".demo")
			os.WriteFile(filepath.Join(state, "memory/a.md"), []byte("keep"), 0600)
			if scenario == "expired" {
				p.Expires = time.Now().Add(-time.Second)
			}
			if scenario == "profile_changed" {
				file := filepath.Join(s.Dirs.Data, "profiles/demo.yaml")
				b, _ := os.ReadFile(file)
				os.WriteFile(file, []byte(strings.Replace(string(b), "display_name: Demo", "display_name: Changed", 1)), 0600)
			}
			if scenario == "directory_changed" {
				if err := os.Rename(state, state+"-moved"); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Join(state, "memory"), 0700); err != nil {
					t.Fatal(err)
				}
				os.WriteFile(filepath.Join(state, "memory/a.md"), []byte("keep"), 0600)
			}
			called := false
			human := proceedConfirm()
			human.Swap = func(*revive.StageVerifyResult) bool {
				called = true
				if scenario == "declined" {
					return false
				}
				if scenario == "tampered_stage" {
					dir := filepath.Join(home, ".valise-stage-"+p.Revive.Plan.OpID+"-state", "memory/a.md")
					if err := os.WriteFile(dir, []byte("tampered"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				return true
			}
			_, err := s.RunRestore(context.Background(), p, human, nil)
			if err == nil {
				t.Fatal("unexpected swap")
			}
			if scenario == "expired" || scenario == "profile_changed" || scenario == "directory_changed" {
				if called {
					t.Fatal("started a stale plan")
				}
			}
			if readFile(t, filepath.Join(state, "memory/a.md")) != "keep" {
				t.Fatal("live state changed")
			}
		})
	}
}

func TestDesktopRestoreBindsVaultAndHeader(t *testing.T) {
	s, p, _ := desktopRestorePlan(t)
	for _, field := range []string{"vault", "head"} {
		req := p.Request
		if field == "vault" {
			req.Snapshot.VaultID = "wrong"
		} else {
			req.Source.Copy.HeaderDigest = vault.SHA256Hex([]byte("wrong"))
		}
		if _, err := s.PrepareRestore(context.Background(), req); err == nil {
			t.Fatal("accepted changed " + field)
		}
	}
	point := journey.TimelinePoint{MetadataState: "conflict", Copies: []journey.Copy{p.Request.Source.Copy}}
	if len(s.RestoreSources(point)) != 0 {
		t.Fatal("conflict offered recovery")
	}
}

func TestDesktopRestoreInterruptedQueriesJournalBeforeBranch(t *testing.T) {
	s, p, home := desktopRestorePlan(t)
	live := filepath.Join(home, ".demo/memory/a.md")
	os.WriteFile(live, []byte("live"), 0600)
	if _, err := s.ExecuteReviveConfirmed(context.Background(), p.Revive, "crash-at="+revive.StateLiveMoved, proceedConfirm()); err == nil {
		t.Fatal("crash not injected")
	}
	task := RestoreTask{OpID: p.Revive.Plan.OpID, Target: "box", From: "box", Mode: revive.ModeRollback, Instance: p.Revive.Plan.InstanceID, Snapshot: p.Request.Snapshot, Created: time.Now()}
	observed, review, err := s.InspectRestore(context.Background(), task)
	if err != nil || observed.State != "needs_recovery" || review == nil {
		t.Fatalf("inspect %+v %v", observed, err)
	}
	// A changed journal invalidates this exact reviewed branch.
	file := filepath.Join(home, ".local/state/valise/journal", task.OpID+".json")
	data, _ := os.ReadFile(file)
	var journal revive.Journal
	if err = json.Unmarshal(data, &journal); err != nil {
		t.Fatal(err)
	}
	journal.Error = "changed after review"
	data, _ = json.Marshal(journal)
	os.WriteFile(file, data, 0600)
	if _, err = s.ResumeRestore(context.Background(), task, review, "back"); err == nil {
		t.Fatal("stale recovery review executed")
	}
	observed, review, err = s.InspectRestore(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	observed, err = s.ResumeRestore(context.Background(), observed, review, "back")
	if err != nil || observed.SwapState != revive.StateRolledBack || observed.State != "failed" {
		t.Fatalf("back %+v %v", observed, err)
	}
	if readFile(t, live) != "live" {
		t.Fatal("back replayed recovery instead of restoring old directory")
	}
	list, err := s.ListRestoreTasks()
	if err != nil || len(list) != 1 || list[0].OpID != task.OpID {
		t.Fatalf("index %+v %v", list, err)
	}
}

func TestDesktopRestoreSecondConfirmationExpiryAndRenewal(t *testing.T) {
	for _, renew := range []bool{false, true} {
		t.Run(fmt.Sprint(renew), func(t *testing.T) {
			s, p, _ := desktopRestorePlan(t)
			human := proceedConfirm()
			human.Swap = func(*revive.StageVerifyResult) bool {
				p.mu.Lock()
				p.stageExpires = time.Now().Add(-time.Second)
				p.mu.Unlock()
				if renew {
					if err := s.RecheckRestoreStage(context.Background(), p); err != nil {
						t.Fatal(err)
					}
				}
				return true
			}
			result, err := s.RunRestore(context.Background(), p, human, nil)
			if renew && (err != nil || result.State != "succeeded") {
				t.Fatalf("renew %+v %v", result, err)
			}
			if !renew && err == nil {
				t.Fatal("expired confirmation exchanged directories")
			}
		})
	}
}
