package revive

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"valise/porter/internal/activationrecord"
)

func TestReusableWorkspaceRequiresReviewedOwnerAndUnchangedCreationIdentity(t *testing.T) {
	w, old := preparationFixture(t)
	if err := savePreparation(w.home, old, true); err != nil {
		t.Fatal(err)
	}
	path, err := preparationWorkspace(w.home, old, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.r.claimPreparation(old, "workspace", "", path, nil); err != nil {
		t.Fatal(err)
	}
	next := &EnvironmentPreparation{Params: old.Params, ManagedSource: &activationrecord.Source{}, Reuse: &preparationReuse{Owners: []reuseOwner{{Operation: old.OperationID}}}}
	check := func(want bool) {
		t.Helper()
		entries, err := os.ReadDir(filepath.Dir(path))
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.Name() == old.OperationID {
				if got := reusablePreparationWorkspace(w.home, next, entry); got != want {
					t.Fatal("workspace ownership mismatch", got, want)
				}
				return
			}
		}
		t.Fatal("workspace not found")
	}
	check(true)
	next.Reuse.Owners[0].Operation = strings.Repeat("e", 32)
	check(false)
	next.Reuse.Owners[0].Operation = old.OperationID
	if err := os.Rename(path, path+"-original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	check(false)
	if err := os.Rename(path, path+"-replacement"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path+"-original", path); err != nil {
		t.Fatal(err)
	}
	check(false)
}
