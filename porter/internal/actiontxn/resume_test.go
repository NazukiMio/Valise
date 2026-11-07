package actiontxn

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"valise/core/action"
	"valise/porter/internal/actionjournal"
	"valise/porter/internal/seal"
)

func TestPublishedConfigurationSurvivesServiceFailureAndResume(t *testing.T) {
	r, _, rec := fixture(t, "active")
	marker := filepath.Join(r.Env.Home, "fail-start")
	if err := os.WriteFile(marker, nil, 0600); err != nil {
		t.Fatal(err)
	}
	res := execute(t, r)
	j := res.Operation
	if res.Exit == 0 || !j.Published || j.Done || j.RecoveryFailure == "" {
		t.Fatalf("%+v", j)
	}
	state, _ := os.ReadFile(filepath.Join(r.Env.Home, "active"))
	if string(state) != "failed" || j.RestoreIntent != "example.service" {
		t.Fatal("failed start lost its recovery intent", string(state), j.RestoreIntent)
	}
	before, _ := os.ReadFile(filepath.Join(rec.Roots["state"], "config.json"))
	if !strings.Contains(string(before), "allowlist") {
		t.Fatal("published configuration was reverted")
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	resumed, err := r.Resume(context.Background(), action.ResumeParams{OperationID: j.ID, Choice: action.ResumeRestore})
	if err != nil || resumed.Exit != 0 || !resumed.Operation.Done || resumed.Operation.Services != "restored" {
		t.Fatal(resumed, err)
	}
	after, _ := os.ReadFile(filepath.Join(rec.Roots["state"], "config.json"))
	if string(before) != string(after) || len(resumed.Operation.Steps) != 1 {
		t.Fatal("resume repeated configuration")
	}
	repeated, err := r.Resume(context.Background(), action.ResumeParams{OperationID: j.ID, Choice: action.ResumeRestore})
	if err != nil || repeated.Exit != resumed.Exit {
		t.Fatal("resume not idempotent", err)
	}
}
func TestCleanupRequiresConfirmationAndKeepsAuditRecord(t *testing.T) {
	r, _, rec := fixture(t, "inactive")
	j := execute(t, r).Operation
	listed, err := r.Cleanup(context.Background(), action.CleanupParams{OperationID: j.ID})
	if err != nil || len(listed) != 1 {
		t.Fatal(listed, err)
	}
	if _, err = os.Stat(j.Previous); err != nil {
		t.Fatal("preview removed previous directory", err)
	}
	removed, err := r.Cleanup(context.Background(), action.CleanupParams{OperationID: j.ID, Confirm: true})
	if err != nil || len(removed) != 1 {
		t.Fatal(removed, err)
	}
	if _, err = os.Stat(j.Previous); !os.IsNotExist(err) {
		t.Fatal("previous directory remains", err)
	}
	if _, err = os.Stat(rec.Roots["state"]); err != nil {
		t.Fatal("active directory removed", err)
	}
	saved, err := (actionjournal.Store{Home: r.Env.Home}).Load(j.ID)
	if err != nil || !saved.Cleaned {
		t.Fatal("operation record lost", err)
	}
}
func TestUnknownStepNeverReruns(t *testing.T) {
	r, _, _ := fixture(t, "active")
	j := execute(t, r).Operation
	// Model a durable started record from an interrupted, unpublished run.
	// Prepare it from the original prev without touching its contents.
	if err := os.Rename(j.Plan.Live, j.Stage); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(j.Previous, j.Plan.Live); err != nil {
		t.Fatal(err)
	}
	j.Done = false
	j.Published = false
	j.StageVerified = false
	j.Phase = "step-started"
	j.Steps[0].Completed = false
	j.Steps[0].Exit = nil
	j.Restored = nil
	j.Services = "pending"
	if err := os.WriteFile(filepath.Join(r.Env.Home, "active"), []byte("inactive"), 0600); err != nil {
		t.Fatal(err)
	}
	store := actionjournal.Store{Home: r.Env.Home}
	if err := store.Save(j); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resume(context.Background(), action.ResumeParams{OperationID: j.ID, Choice: action.ResumeContinue}); err == nil {
		t.Fatal("unknown step continued")
	}
	restored, err := r.Resume(context.Background(), action.ResumeParams{OperationID: j.ID, Choice: action.ResumeRestore})
	if err != nil || restored.Operation.Published || restored.Operation.Services != "restored" {
		t.Fatal(restored, err)
	}
	if restored.Operation.Steps[0].Completed {
		t.Fatal("unknown step reran")
	}
}
func TestPendingOperationBlocksSealLock(t *testing.T) {
	r, _, _ := fixture(t, "inactive")
	j := execute(t, r).Operation
	j.Done = false
	if err := (actionjournal.Store{Home: r.Env.Home}).Save(j); err != nil {
		t.Fatal(err)
	}
	if unlock, err := (seal.Paths{Home: r.Env.Home}).Lock(); err == nil {
		unlock()
		t.Fatal("seal ignored pending action")
	}
}
