package environments

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"valise/core/environment"
	"valise/core/identity"
)

func TestCASConcurrentWritersAndIdentity(t *testing.T) {
	s := Store{Home: t.TempDir()}
	a := identity.AgentRef{ServerUID: "server", InstallID: "install", InstanceID: "agent", Generation: "generation"}
	if _, err := s.Load(a); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("read initialized state", err)
	}
	r, err := s.Update(a, 0, func(environment.Registry) (environment.Registry, error) { return environment.New(a) })
	if err != nil {
		t.Fatal(err)
	}
	const writers = 12
	var wg sync.WaitGroup
	results := make(chan error, writers)
	for range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Update(a, r.Revision, func(r environment.Registry) (environment.Registry, error) { return r.SetMode(environment.Compact) })
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
	loaded, err := s.Load(a)
	if err != nil || loaded.Mode != environment.Compact || loaded.Revision != r.Revision+1 {
		t.Fatal(loaded, err)
	}
	st, err := os.Stat(s.path(a))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatal("registry permissions", err)
	}
	other := a
	other.Generation = "other"
	if _, err := s.Load(other); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("registry shared by generations")
	}
	if _, err := s.Update(a, loaded.Revision, func(r environment.Registry) (environment.Registry, error) {
		r.Agent = other
		r.Revision++
		return r, nil
	}); err == nil {
		t.Fatal("CAS allowed identity replacement")
	}
	if err := os.WriteFile(s.path(a), []byte(strings.Repeat("garbled", 20)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(a, 0, func(environment.Registry) (environment.Registry, error) { return environment.New(a) }); err == nil {
		t.Fatal("corrupt record silently reinitialized")
	}
}

func TestRejectsExposedOrAliasedRecords(t *testing.T) {
	for _, mutate := range []string{"permissions", "symlink", "hardlink", "trailing_json", "unknown_field"} {
		t.Run(mutate, func(t *testing.T) {
			s := Store{Home: t.TempDir()}
			a := identity.AgentRef{ServerUID: "server", InstallID: "install", InstanceID: "agent", Generation: "generation"}
			if _, err := s.Update(a, 0, func(environment.Registry) (environment.Registry, error) { return environment.New(a) }); err != nil {
				t.Fatal(err)
			}
			p := s.path(a)
			var err error
			switch mutate {
			case "permissions":
				err = os.Chmod(p, 0o644)
			case "symlink":
				err = os.Rename(p, p+".original")
				if err == nil {
					err = os.Symlink(p+".original", p)
				}
			case "hardlink":
				err = os.Link(p, filepath.Join(t.TempDir(), "alias"))
			case "trailing_json", "unknown_field":
				var b []byte
				b, err = os.ReadFile(p)
				if err == nil {
					if mutate == "trailing_json" {
						b = append(b, []byte(" {}")...)
					} else {
						b = append([]byte(`{"untrusted":true,`), b[1:]...)
					}
					err = os.WriteFile(p, b, 0o600)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Load(a); err == nil {
				t.Fatal("accepted invalid record")
			}
		})
	}
}
