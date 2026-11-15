package environments

import (
	"errors"
	"os"
	"strings"
	"testing"

	"valise/core/environment"
)

func TestReleaseInitialPreservesLatestPolicyAndRejectsRealRecovery(t *testing.T) {
	s, agent, in, _ := bindingFixture(t)
	registry := initialRegistry(t, s, agent)
	op := strings.Repeat("a", 32)
	var err error
	registry, err = (Store{Home: s.Home}).Update(agent, registry.Revision, func(v environment.Registry) (environment.Registry, error) {
		return v.Begin(op, in.Target.Source.Snapshot)
	})
	if err != nil {
		t.Fatal(err)
	}
	store := Store{Home: s.Home}
	if err := store.ReleaseInitial(agent, registry.Revision, op); err == nil {
		t.Fatal("unfinished preparation released")
	}
	registry, err = store.Update(agent, registry.Revision, func(v environment.Registry) (environment.Registry, error) { return v.CancelPreparation(op) })
	if err != nil {
		t.Fatal(err)
	}
	settings, err := store.Settings(agent)
	if err != nil || settings.Busy {
		t.Fatal(settings, err)
	}
	saved, err := store.SetSettings(environment.SetSettingsParams{Agent: agent, Expected: settings.Digest, Mode: environment.Compact})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ReleaseInitial(agent, registry.Revision, op); !errors.Is(err, ErrConflict) {
		t.Fatal("stale revision released", err)
	}
	if err := store.ReleaseInitial(agent, saved.Revision, strings.Repeat("b", 32)); err == nil {
		t.Fatal("another operation released registry")
	}
	if err := store.ReleaseInitial(agent, saved.Revision, op); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(agent); !os.IsNotExist(err) {
		t.Fatal("initial registry remained", err)
	}
	after, err := store.Settings(agent)
	if err != nil || after.Managed || after.Mode != environment.Compact || after.Revision <= saved.Revision || after.Digest == settings.Digest {
		t.Fatal(after, err)
	}
	if _, err := store.SetSettings(environment.SetSettingsParams{Agent: agent, Expected: settings.Digest, Mode: environment.Direct}); !errors.Is(err, ErrConflict) {
		t.Fatal("stale managed settings survived release", err)
	}
}
