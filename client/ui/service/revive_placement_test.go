package service

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"valise/client/config"
	"valise/core/revive"
	"valise/core/vault"
	"valise/core/vaultrpc"
	"valise/testbed/fakesshenv"
)

const relocatableDemoProfile = `schema: valise-profile/1
id: demo
display_name: Demo
profile_version: 1
status: draft
detect:
  binaries: [demo]
  state_dir_candidates: ["${HOME}/.demo"]
  markers_any: [demo.json]
  named:
    - glob: "${HOME}/.demo-*"
      env: { DEMO_HOME: "${STATE_DIR}" }
roots:
  - id: workspace
    default: "${HOME}/demo-work"
relocation:
  env: DEMO_HOME
paths:
  - { root: state, glob: ".env", category: secrets }
  - { root: state, glob: "demo.json", category: config }
  - { root: state, glob: "memory/**", category: memory }
  - { root: workspace, glob: "**", category: workspace }
provenance: { kind: binary, binary: { name: demo } }
`

func TestInspectTargetPlacementDailyOnlyAndCollisionAvoidance(t *testing.T) {
	e := fakesshenv.New(t)
	homeA := e.AddHost("alpha")
	homeB := e.AddHost("beta")
	e.InstallPorter("alpha")
	e.InstallPorter("beta")

	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Alpha has default + named instance "work".
	write(filepath.Join(homeA, ".demo/demo.json"), "{}")
	write(filepath.Join(homeA, ".demo-work/demo.json"), "{}")
	write(filepath.Join(homeA, ".local/bin/demo"), "#!/bin/sh\necho demo 1.0\n")
	// Beta already has default + demo-clone occupied.
	write(filepath.Join(homeB, ".demo/demo.json"), "{}")
	write(filepath.Join(homeB, ".demo-clone/demo.json"), "{}")
	write(filepath.Join(homeB, ".local/bin/demo"), "#!/bin/sh\necho demo 1.0\n")

	s := newService(t, e.SSH)
	write(filepath.Join(s.Dirs.Data, "profiles/demo.yaml"), relocatableDemoProfile)

	ctx := context.Background()
	if _, err := s.Enroll(ctx, "alpha", EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enroll(ctx, "alpha", EnrollOptions{ProfileID: "demo", Name: "work", Accept: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enroll(ctx, "beta", EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enroll(ctx, "beta", EnrollOptions{ProfileID: "demo", Name: "clone", InstanceID: "demo-clone", Accept: true}); err != nil {
		t.Fatal(err)
	}

	// Mark beta as tiered so any owner RPC would trigger OwnerAuth.
	if err := config.SaveServerList(s.Dirs.Data, []config.Server{
		{Name: "alpha", Source: config.SourceSSHConfig},
		{Name: "beta", Source: config.SourceValise, HostName: "127.0.0.1", Port: 22, User: "me"},
	}); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(config.KeyDir(s.Dirs.Data, "beta"), "daily")
	write(keyPath, "fake-key")
	write(keyPath+".pub", "ssh-ed25519 AAAATEST daily")
	write(filepath.Join(homeB, ".ssh/authorized_keys"), `restrict,pty,command="$HOME/.local/bin/valise-porter serve --tier daily" ssh-ed25519 AAAATEST daily`+"\n")

	ownerPrompted := false
	s.OwnerAuth.Passphrase = func(string) (string, error) {
		ownerPrompted = true
		return "", nil
	}

	// 1. Same-host clone on beta: CanReplaceDefault=false, .demo-clone occupied -> recommends .demo-clone-2.
	p1, err := s.InspectTargetPlacement(ctx, TargetPlacementRequest{
		From:               "beta",
		Target:             "beta",
		Mode:               revive.ModeClone,
		ProfileID:          "demo",
		RecordedInstanceID: "demo",
		DesiredInstanceID:  "demo-clone",
	})
	if err != nil {
		t.Fatalf("InspectTargetPlacement same-host clone: %v", err)
	}
	if ownerPrompted {
		t.Fatal("InspectTargetPlacement must never trigger owner passphrase prompt")
	}
	if p1.TargetHome != homeB || p1.DefaultStateDir != filepath.Join(homeB, ".demo") {
		t.Fatalf("unexpected paths: %+v", p1)
	}
	if p1.CanReplaceDefault || !p1.Relocatable {
		t.Fatalf("unexpected flags: %+v", p1)
	}
	if !slices.Equal(p1.ExtraRootIDs, []string{"workspace"}) {
		t.Fatalf("unexpected extra roots: %v", p1.ExtraRootIDs)
	}
	if !slices.Equal(p1.DefaultOccupiedBy, []string{"demo"}) {
		t.Fatalf("unexpected DefaultOccupiedBy: %v", p1.DefaultOccupiedBy)
	}
	wantRec := filepath.Join(homeB, ".demo-clone-2")
	if p1.RecommendedStateDir != wantRec {
		t.Fatalf("expected recommended %q, got %q", wantRec, p1.RecommendedStateDir)
	}

	// 2. Cross-server migrate of named source "work" from alpha to beta:
	// picks SourceName="work", SourceRoots populated, CanReplaceDefault=true, recommends .demo-work.
	p2, err := s.InspectTargetPlacement(ctx, TargetPlacementRequest{
		From:               "alpha",
		Target:             "beta",
		Mode:               revive.ModeMigrate,
		ProfileID:          "demo",
		RecordedInstanceID: "demo-work",
	})
	if err != nil {
		t.Fatalf("InspectTargetPlacement cross-server named: %v", err)
	}
	if ownerPrompted {
		t.Fatal("InspectTargetPlacement must never trigger owner passphrase prompt")
	}
	if p2.SourceName != "work" || p2.SourceRoots["state"] != filepath.Join(homeA, ".demo-work") {
		t.Fatalf("expected source name & roots from alpha, got %+v", p2)
	}
	if !p2.CanReplaceDefault || p2.RecommendedStateDir != filepath.Join(homeB, ".demo-work") {
		t.Fatalf("unexpected migrate placement: %+v", p2)
	}

	// 3. Unreachable source does not fail InspectTargetPlacement.
	p3, err := s.InspectTargetPlacement(ctx, TargetPlacementRequest{
		From:               "unreachable-host",
		Target:             "beta",
		Mode:               revive.ModeMigrate,
		ProfileID:          "demo",
		RecordedInstanceID: "demo",
	})
	if err != nil {
		t.Fatalf("InspectTargetPlacement with unreachable source: %v", err)
	}
	if p3.RecommendedStateDir != filepath.Join(homeB, ".demo-copy") {
		t.Fatalf("expected .demo-copy fallback, got %+v", p3)
	}
}

func TestPlanReviveSoulSkippedUnclassifiedWarning(t *testing.T) {
	s, _ := enrollWorld(t)
	ctx := context.Background()
	const pass = "test-passphrase-1234"
	if _, err := s.KeyInit(pass); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.KeyRegister(ctx, "box", ""); err != nil {
		t.Fatal(err)
	}
	// enrollWorld puts .demo/notes/x.md which is unclassified.
	if _, err := s.Enroll(ctx, "box", EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatal(err)
	}
	rep, err := s.Seal(ctx, "box", vaultrpc.SealParams{InstanceID: "demo", Tier: vault.TierSoul})
	if err != nil {
		t.Fatal(err)
	}
	if rep.UnclassifiedCount != 1 {
		t.Fatalf("expected 1 unclassified file in seal report, got %+v", rep)
	}
	plan, err := s.PlanRevive(ctx, ReviveOptions{
		Target:     "box",
		From:       "box",
		SnapshotID: rep.SnapshotID,
		Mode:       revive.ModeRollback,
		AllowDraft: true,
	}, pass)
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Close()
	if plan.SkippedUnclassified != 1 {
		t.Fatalf("expected SkippedUnclassified=1, got %d", plan.SkippedUnclassified)
	}
	foundWarn := false
	for _, w := range plan.Warnings {
		if strings.Contains(w, "轻装备份") && strings.Contains(w, "1 个尚未归类的文件") {
			foundWarn = true
			break
		}
	}
	if !foundWarn {
		t.Fatalf("expected soul unclassified warning, got %v", plan.Warnings)
	}
}
