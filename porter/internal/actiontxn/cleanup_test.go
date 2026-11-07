package actiontxn

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"valise/core/action"
	"valise/porter/internal/actionjournal"
	porterenroll "valise/porter/internal/enroll"
)

func TestCleanupCrashAfterWrapperRemovalCanComplete(t *testing.T) {
	r, _, _ := fixture(t, "inactive")
	j := execute(t, r).Operation
	// Crash after the explicit cleanup has removed its last directory, before Save.
	j.CleanupStarted = true
	store := actionjournal.Store{Home: r.Env.Home}
	if err := store.Save(j); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(j.Previous); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Dir(j.Stage)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Cleanup(context.Background(), action.CleanupParams{OperationID: j.ID, Confirm: true}); err != nil {
		t.Fatal(err)
	}
	saved, err := store.Load(j.ID)
	if err != nil || !saved.Cleaned || saved.Phase != "complete" {
		t.Fatal(saved, err)
	}
	resumed, err := r.Resume(context.Background(), action.ResumeParams{OperationID: j.ID, Choice: action.ResumeRestore})
	if err != nil || resumed.Exit != 0 {
		t.Fatal("cleanup changed successful result", resumed, err)
	}
}
func TestCleanupRejectsReRegisteredRetainedDirectory(t *testing.T) {
	r, p, rec := fixture(t, "inactive")
	j := execute(t, r).Operation
	rec.Roots["state"] = j.Previous
	if _, err := porterenroll.WriteRecord(r.Env.Home, rec, *p); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Cleanup(context.Background(), action.CleanupParams{OperationID: j.ID, Confirm: true}); err == nil {
		t.Fatal("removed registered root")
	}
	if _, err := os.Stat(j.Previous); err != nil {
		t.Fatal(err)
	}
}
func TestExternalFailedServiceIsNotReset(t *testing.T) {
	r, _, _ := fixture(t, "active")
	j := execute(t, r).Operation
	j.Done = false
	j.Services = "remaining"
	j.RestoreIntent = ""
	if err := (actionjournal.Store{Home: r.Env.Home}).Save(j); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.Env.Home, "active"), []byte("failed"), 0600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(r.Env.Home, "calls"))
	res, err := r.Resume(context.Background(), action.ResumeParams{OperationID: j.ID, Choice: action.ResumeRestore})
	if err == nil && (res.Exit == 0 || res.Operation.Done) {
		t.Fatal("external failure treated as own", res)
	}
	after, _ := os.ReadFile(filepath.Join(r.Env.Home, "calls"))
	if string(before) != string(after) {
		t.Fatal("external failure caused service commands")
	}
}
