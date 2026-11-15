package service

import (
	"context"
	"path/filepath"
	"testing"

	"valise/core/profile"
	"valise/core/revive"
	"valise/core/vault"
)

func TestCurrentRestoreIgnoresUserPathsAndRequiresEveryRegisteredRoot(t *testing.T) {
	s, p, home := desktopRestorePlan(t)
	defer p.Revive.Close()
	req := p.Request
	req.Options.Roots = map[string]string{"state": filepath.Join(t.TempDir(), "wrong")}
	p2, err := s.PrepareRestore(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	defer p2.Revive.Close()
	if len(p2.Revive.Plan.Roots) != 1 || p2.Revive.Plan.Roots[0].Path != filepath.Join(home, ".demo") {
		t.Fatalf("current registration ignored: %+v", p2.Revive.Plan.Roots)
	}
	p2.Revive.Plan.Roots = append(p2.Revive.Plan.Roots, revive.RootPlan{ID: "unregistered", Path: filepath.Join(home, "obsolete")})
	if err := currentRestorePlan(p2.Revive, p2.Revive.Plan.InstanceID, map[string]string{"state": filepath.Join(home, ".demo")}); journeyCode(err) != "root_invalid" {
		t.Fatalf("fallback to old snapshot path: %v", err)
	}
}

func TestRestoreNestedWorkspaceAlwaysUsesSingleOuterExchange(t *testing.T) {
	h := vault.Snapshot{Roots: map[string]string{"state": "/home/user/state", "workspace": "/home/user/state/workspace", "deep": "/home/user/state/workspace/deep"}}
	for range 40 {
		roots, err := mapRoots(ReviveOptions{Mode: revive.ModeRollback}, &h, revive.InspectResult{}, profile.Profile{})
		if err != nil {
			t.Fatal(err)
		}
		for _, root := range roots {
			if root.ID == "deep" && (root.SwapRoot != "state" || root.Rel != "workspace/deep") {
				t.Fatal("nested transfer would target a nonphysical root", roots)
			}
		}
	}
	h.Roots["workspace"] = h.Roots["state"]
	if _, err := mapRoots(ReviveOptions{Mode: revive.ModeRollback}, &h, revive.InspectResult{}, profile.Profile{}); journeyCode(err) != "root_layout_invalid" {
		t.Fatal("ambiguous content directories lack actionable error", err)
	}
}
