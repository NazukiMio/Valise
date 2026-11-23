package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSavedInstallsRoundTrip(t *testing.T) {
	dirs := Dirs{Data: t.TempDir()}

	// Missing file returns empty list.
	list, err := dirs.LoadSavedInstalls()
	if err != nil || len(list) != 0 {
		t.Fatalf("initial LoadSavedInstalls = %+v, err=%v", list, err)
	}

	// Empty Raw is rejected.
	if err := dirs.SaveSavedInstall(SavedInstall{Name: "Empty"}); err == nil {
		t.Fatal("expected error for empty Raw")
	}

	t1 := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	if err := dirs.SaveSavedInstall(SavedInstall{
		Name:      "Hermes Official Installer",
		Raw:       "curl -fsSL https://hermes-agent.nousresearch.com/install.sh | bash",
		ProfileID: "hermes",
		SavedAt:   t1,
	}); err != nil {
		t.Fatalf("SaveSavedInstall: %v", err)
	}

	info, err := os.Stat(filepath.Join(dirs.Data, CustomInstallsFile))
	if err != nil {
		t.Fatalf("stat %s: %v", CustomInstallsFile, err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("permissions = %o, want 0600", perm)
	}

	list, err = dirs.LoadSavedInstalls()
	if err != nil || len(list) != 1 {
		t.Fatalf("LoadSavedInstalls after first save = %+v, err=%v", list, err)
	}
	firstID := list[0].ID
	if firstID == "" || list[0].Name != "Hermes Official Installer" || list[0].ProfileID != "hermes" || !list[0].SavedAt.Equal(t1) {
		t.Fatalf("unexpected saved item: %+v", list[0])
	}

	// Saving the same Raw+ProfileID updates in place and keeps the ID.
	t2 := t1.Add(time.Hour)
	if err := dirs.SaveSavedInstall(SavedInstall{
		Name:      "Hermes Installer (Updated Label)",
		Raw:       "curl -fsSL https://hermes-agent.nousresearch.com/install.sh | bash",
		ProfileID: "hermes",
		SavedAt:   t2,
	}); err != nil {
		t.Fatalf("SaveSavedInstall update: %v", err)
	}
	list, err = dirs.LoadSavedInstalls()
	if err != nil || len(list) != 1 || list[0].ID != firstID || list[0].Name != "Hermes Installer (Updated Label)" {
		t.Fatalf("expected in-place update, got %+v (err=%v)", list, err)
	}

	// Add a second distinct preset.
	if err := dirs.SaveSavedInstall(SavedInstall{
		ID:   "ci_second",
		Raw:  "https://openclaw.ai/install.sh",
		Name: "OpenClaw Script",
	}); err != nil {
		t.Fatalf("SaveSavedInstall second: %v", err)
	}
	list, err = dirs.LoadSavedInstalls()
	if err != nil || len(list) != 2 {
		t.Fatalf("expected 2 items, got %+v (err=%v)", list, err)
	}

	// Delete the first preset.
	if err := dirs.DeleteSavedInstall(firstID); err != nil {
		t.Fatalf("DeleteSavedInstall: %v", err)
	}
	list, err = dirs.LoadSavedInstalls()
	if err != nil || len(list) != 1 || list[0].ID != "ci_second" {
		t.Fatalf("after DeleteSavedInstall = %+v, err=%v", list, err)
	}
}
