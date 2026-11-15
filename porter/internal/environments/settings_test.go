package environments

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"strings"
	"valise/core/environment"
	"valise/core/identity"
	"valise/core/vault"
)

func TestSettingsPersistWithoutInventingRuntimeAndSurviveAdoption(t *testing.T) {
	s := Store{Home: t.TempDir()}
	a := identity.AgentRef{ServerUID: "server", InstallID: "install", InstanceID: "agent", Generation: "one"}
	v, err := s.Settings(a)
	if err != nil || v.Mode != environment.Parallel || v.Managed || v.Revision != 0 {
		t.Fatal(v, err)
	}
	if _, err := os.Stat(s.dir()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("read created state", err)
	}
	old := v
	v, err = s.SetSettings(environment.SetSettingsParams{Agent: a, Expected: v.Digest, Mode: environment.Compact})
	if err != nil || v.Managed || v.Mode != environment.Compact || v.Revision != 1 {
		t.Fatal(v, err)
	}
	if _, err := s.Load(a); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("settings fabricated active A", err)
	}
	if _, err := s.SetSettings(environment.SetSettingsParams{Agent: a, Expected: old.Digest, Mode: environment.Direct}); !errors.Is(err, ErrConflict) {
		t.Fatal("stale settings saved", err)
	}
	loaded, err := (Store{Home: s.Home}).Settings(a)
	if err != nil || loaded != v {
		t.Fatal("reopen lost settings", loaded, err)
	}
	r, err := s.Update(a, 0, func(environment.Registry) (environment.Registry, error) { return environment.New(a) })
	if err != nil || r.Mode != environment.Compact {
		t.Fatal("adoption ignored saved mode", r, err)
	}
	managed, err := s.Settings(a)
	if err != nil || !managed.Managed || managed.Mode != v.Mode || managed.Digest == v.Digest {
		t.Fatal("authority not bound", managed, err)
	}
	if _, err := s.SetSettings(environment.SetSettingsParams{Agent: a, Expected: v.Digest, Mode: environment.Direct}); !errors.Is(err, ErrConflict) {
		t.Fatal("old preference view mutated registry", err)
	}
	managed, err = s.SetSettings(environment.SetSettingsParams{Agent: a, Expected: managed.Digest, Mode: environment.Parallel})
	if err != nil || managed.Revision != 2 {
		t.Fatal(managed, err)
	}
	r, err = s.Update(a, managed.Revision, func(r environment.Registry) (environment.Registry, error) {
		return r.Begin(strings.Repeat("a", 32), identity.SnapshotRef{VaultID: vault.NewID("v", time.Now()), SnapshotID: vault.NewID("s", time.Now())})
	})
	if err != nil {
		t.Fatal(err)
	}
	busy, err := s.Settings(a)
	if err != nil || !busy.Busy {
		t.Fatal(busy, err)
	}
	if _, err := s.SetSettings(environment.SetSettingsParams{Agent: a, Expected: busy.Digest, Mode: environment.Direct}); !errors.Is(err, environment.ErrState) {
		t.Fatal("in-flight mode changed", err)
	}
	other := a
	other.Generation = "two"
	fresh, err := s.Settings(other)
	if err != nil || fresh.Mode != environment.Parallel || fresh.Managed {
		t.Fatal("generation inherited policy", fresh, err)
	}
}

func TestSettingsConcurrentCASAndAliasedFiles(t *testing.T) {
	s := Store{Home: t.TempDir()}
	a := identity.AgentRef{ServerUID: "server", InstallID: "install", InstanceID: "agent", Generation: "one"}
	v, _ := s.Settings(a)
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.SetSettings(environment.SetSettingsParams{Agent: a, Expected: v.Digest, Mode: environment.Compact})
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
	}
	if winners != 1 {
		t.Fatal("CAS winners", winners)
	}
	if err := os.Link(s.policyPath(a), filepath.Join(t.TempDir(), "alias")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Settings(a); err == nil {
		t.Fatal("hard-linked settings accepted")
	}
	alias := Store{Home: t.TempDir()}
	if err := os.Symlink(s.Home, filepath.Join(alias.Home, ".local")); err != nil {
		t.Fatal(err)
	}
	if _, err := alias.Settings(a); err == nil {
		t.Fatal("aliased ancestor accepted")
	}
}
