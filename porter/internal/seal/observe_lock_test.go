package seal

import (
	"os"
	"path/filepath"
	"testing"

	"valise/core/valise"
)

func TestObservationLockDoesNotCreateOrRewriteAndStillExcludesMutations(t *testing.T) {
	p := Paths{Home: t.TempDir()}
	if _, err := p.ObservationLock(); !os.IsNotExist(err) {
		t.Fatal("missing observation lock was created", err)
	}
	if entries, _ := os.ReadDir(p.Home); len(entries) != 0 {
		t.Fatal("observation initialized private directories")
	}
	if err := os.MkdirAll(p.State(), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(p.State(), "vault.lock")
	if err := os.WriteFile(path, []byte("unchanged holder"), 0600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(path)
	unlock, err := p.ObservationLock()
	if err != nil {
		t.Fatal(err)
	}
	if release, err := p.LockFor("other"); err == nil {
		release()
		t.Fatal("observation did not exclude a directory mutation")
	} else if e, ok := valise.As(err); !ok || e.Code != "vault_busy" {
		t.Fatal(err)
	}
	unlock()
	after, _ := os.Stat(path)
	b, _ := os.ReadFile(path)
	if string(b) != "unchanged holder" || before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("query rewrote lock holder metadata")
	}
}
