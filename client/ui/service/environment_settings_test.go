package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"valise/core/environment"
	"valise/core/identity"
	"valise/core/rpc"
)

func TestEnvironmentSettingsOverFakeSSHAndDirectPlanRecheck(t *testing.T) {
	s, p, home := desktopRestorePlan(t)
	defer p.Revive.Close()
	ctx := context.Background()
	v, err := s.InstanceEnvironmentSettings(ctx, "box", "demo")
	if err != nil || v.Mode != environment.Direct || v.Managed {
		t.Fatal(v, err)
	}
	stale := v
	v, err = s.SaveEnvironmentSettings(ctx, "box", v, environment.Compact)
	if err != nil || v.Mode != environment.Compact {
		t.Fatal(v, err)
	}
	if _, err := s.SaveEnvironmentSettings(ctx, "box", stale, environment.Parallel); journeyCode(err) != "environment_settings_changed" {
		t.Fatal("stale form saved", err)
	}
	if _, err := s.PrepareRestore(ctx, p.Request); journeyCode(err) != "environment_parallel_unavailable" {
		t.Fatal("parallel used direct plan", err)
	}
	if _, err := s.RunRestore(ctx, p, proceedConfirm(), nil); journeyCode(err) != "environment_parallel_unavailable" {
		t.Fatal("old direct plan ran after settings change", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".local/state/valise/environments", environment.Key(v.Agent)+".json")); !os.IsNotExist(err) {
		t.Fatal("mode save fabricated runtime", err)
	}
	if got := readFile(t, filepath.Join(home, ".demo/memory/a.md")); got != "m" {
		t.Fatal("settings changed live data", got)
	}
	// Reopening the service reads the same server policy, rather than a local
	// preference. No owner credentials need be re-entered for this read.
	other := &Service{Dirs: s.Dirs, Config: s.Config, Home: s.Home}
	reopened, err := other.InstanceEnvironmentSettings(ctx, "box", "demo")
	if err != nil || reopened != v {
		t.Fatal(reopened, err)
	}
	wrong := v
	wrong.Agent.Generation = "other"
	wrong.Digest = wrong.Stamp()
	if _, err := s.SaveEnvironmentSettings(ctx, "box", wrong, environment.Direct); journeyCode(err) != "identity_conflict" {
		t.Fatal("wrong generation changed policy", err)
	}
}

func TestEnvironmentSettingsCapabilitiesAndResponseIdentity(t *testing.T) {
	for _, method := range []string{environment.MethodSettingsGet, environment.MethodSettingsSet} {
		if !rpc.Classified(method) {
			t.Fatal("unclassified", method)
		}
		if n2Compatibility(&rpc.HelloResult{}, method, nil) == nil {
			t.Fatal("old porter accepted settings", method)
		}
		if err := n2Compatibility(&rpc.HelloResult{Capabilities: []string{environment.SettingsCapability}}, method, nil); err != nil {
			t.Fatal(err)
		}
	}
	if rpc.TierOf(environment.MethodSettingsGet) != rpc.TierDaily || rpc.TierOf(environment.MethodSettingsSet) != rpc.TierOwner {
		t.Fatal("wrong settings permissions")
	}
	a := identity.AgentRef{ServerUID: "server", InstallID: "install", InstanceID: "agent", Generation: "one"}
	v := environment.Settings{Agent: a, Mode: environment.Direct}
	v.Digest = v.Stamp()
	if err := validateEnvironmentSettings(v, a); err != nil {
		t.Fatal(err)
	}
	v.Agent.Generation = "two"
	v.Digest = v.Stamp()
	if err := validateEnvironmentSettings(v, a); err == nil {
		t.Fatal("cross-generation response accepted")
	}
	v.Agent = a
	v.Managed = true
	v.Revision = 1
	v.Digest = v.Stamp()
	if err := requireDirectRestore(v); journeyCode(err) != "environment_managed" {
		t.Fatal("managed runtime used legacy direct executor", err)
	}
}
