package protection

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cp "valise/core/protection"
)

func installFakePorter(t *testing.T, home, caps string) {
	t.Helper()
	path := DeployedPorter(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	body := "#!/bin/sh\nprintf '{\"name\":\"valise-porter\",\"version\":\"x\",\"protocol\":\"1.3\",\"os\":\"linux\",\"arch\":\"arm64\",\"self_sha256\":\"\"" + caps + "}\\n'\n"
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
}

func TestCheckDeploymentRequiresDeclaredCapability(t *testing.T) {
	s, _, _ := world(t)
	ctx := context.Background()
	if err := CheckDeployment(ctx, s.Env, s.Home); err == nil {
		t.Fatal("missing installed porter accepted")
	}
	installFakePorter(t, s.Home, "")
	if err := CheckDeployment(ctx, s.Env, s.Home); err == nil {
		t.Fatal("porter without capabilities accepted")
	}
	installFakePorter(t, s.Home, `,"capabilities":["identity/1"]`)
	if err := CheckDeployment(ctx, s.Env, s.Home); err == nil {
		t.Fatal("porter without protection capability accepted")
	}
	installFakePorter(t, s.Home, `,"capabilities":["identity/1","`+cp.Capability+`"]`)
	if err := CheckDeployment(ctx, s.Env, s.Home); err != nil {
		t.Fatal(err)
	}
}

// After a downgrade, an already running process must not record a transaction.
func TestApplyRefusedAfterDowngradeWritesNothing(t *testing.T) {
	s, a, d := world(t)
	installFakePorter(t, s.Home, "")
	s.ValidateDeployment = func(ctx context.Context) error { return CheckDeployment(ctx, s.Env, s.Home) }
	p := prepare(t, s, a, d, 0)
	if _, err := s.Apply(context.Background(), cp.ApplyParams{Plan: *p, ChangeID: strings.Repeat("9", 32)}); err == nil {
		t.Fatal("apply accepted with an incompatible installed porter")
	}
	if entries, _ := os.ReadDir(s.state()); len(entries) > 0 {
		for _, e := range entries {
			if filepath.Ext(e.Name()) == ".json" {
				t.Fatalf("transaction recorded: %s", e.Name())
			}
		}
	}
	if _, err := os.Stat(s.planPath(a.InstanceID)); !os.IsNotExist(err) {
		t.Fatal("plan published")
	}
	installFakePorter(t, s.Home, `,"capabilities":["`+cp.Capability+`"]`)
	if r := apply(t, s, p); !r.Committed {
		t.Fatal(r)
	}
}
