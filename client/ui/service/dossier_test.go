package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"valise/core/enroll"
	"valise/core/profile"
)

func TestUpdateDossierAndInstanceDossierRoundTrip(t *testing.T) {
	s, home := enrollWorld(t)
	ctx := context.Background()
	if _, err := s.AddServers("box"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enroll(ctx, "box", EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatal(err)
	}

	// Populate AgentReader cache to verify UpdateDossier invalidates it.
	if _, err := s.ListAgents(ctx, AgentListRequest{ConnectionIDs: []string{"box"}}); err != nil {
		t.Fatal(err)
	}
	s.agentReader().mu.Lock()
	cachedBefore := len(s.agentReader().cache)
	s.agentReader().mu.Unlock()
	if cachedBefore == 0 {
		t.Fatal("expected agent cache to be populated before UpdateDossier")
	}

	initial, err := s.InstanceDossier(ctx, "box", "demo")
	if err != nil {
		t.Fatalf("InstanceDossier initial: %v", err)
	}
	if initial.InstanceID != "demo" || len(initial.LocalRoots) != 0 || len(initial.ToolDeps) != 0 {
		t.Fatalf("unexpected initial dossier: %+v", initial)
	}

	extraDir := filepath.Join(home, "MioSaid")
	if err := os.MkdirAll(extraDir, 0o755); err != nil {
		t.Fatal(err)
	}
	updated, err := s.UpdateDossier(ctx, "box", enroll.DossierUpdateParams{
		InstanceID: "demo",
		AddRoots: []enroll.LocalRoot{
			{ID: "ext-notes", Path: extraDir, Category: profile.CategoryMemory, Reason: "external notes"},
		},
		AddToolDeps: []enroll.ToolDep{
			{Name: "companion-a", Purpose: "sync notes"},
			{Name: "companion-b", Purpose: "format docs"},
		},
	})
	if err != nil {
		t.Fatalf("UpdateDossier add: %v", err)
	}
	if len(updated.LocalRoots) != 1 || updated.LocalRoots[0].ID != "ext-notes" || updated.Roots["ext-notes"] != extraDir {
		t.Fatalf("unexpected LocalRoots/Roots: %+v %+v", updated.LocalRoots, updated.Roots)
	}
	if len(updated.ToolDeps) != 2 || updated.ToolDeps[0].Name != "companion-a" || updated.ToolDeps[1].Name != "companion-b" {
		t.Fatalf("unexpected ToolDeps: %+v", updated.ToolDeps)
	}

	// Agent cache for "box" must be invalidated by UpdateDossier.
	s.agentReader().mu.Lock()
	cachedAfter := len(s.agentReader().cache)
	s.agentReader().mu.Unlock()
	if cachedAfter != 0 {
		t.Fatalf("expected agent cache invalidated after UpdateDossier, still has %d entries", cachedAfter)
	}

	// Daily-tier InstanceDossier reads back the persisted updates.
	reloaded, err := s.InstanceDossier(ctx, "box", "demo")
	if err != nil {
		t.Fatalf("InstanceDossier reload: %v", err)
	}
	if len(reloaded.LocalRoots) != 1 || reloaded.LocalRoots[0].Path != extraDir || len(reloaded.ToolDeps) != 2 {
		t.Fatalf("reloaded dossier mismatch: %+v", reloaded)
	}

	// Remove one local root and one tool dep.
	trimmed, err := s.UpdateDossier(ctx, "box", enroll.DossierUpdateParams{
		InstanceID:      "demo",
		RemoveRootIDs:   []string{"ext-notes"},
		RemoveToolNames: []string{"companion-b"},
	})
	if err != nil {
		t.Fatalf("UpdateDossier remove: %v", err)
	}
	if len(trimmed.LocalRoots) != 0 || trimmed.Roots["ext-notes"] != "" {
		t.Fatalf("expected ext-notes removed: %+v %+v", trimmed.LocalRoots, trimmed.Roots)
	}
	if len(trimmed.ToolDeps) != 1 || trimmed.ToolDeps[0].Name != "companion-a" {
		t.Fatalf("expected only companion-a remaining: %+v", trimmed.ToolDeps)
	}
}
