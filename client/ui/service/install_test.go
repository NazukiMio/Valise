package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPlanInstallExisting: the plan carries its short summary and the
// harness's data already on the server (docs/UI.md 9.6).
func TestPlanInstallExisting(t *testing.T) {
	s, _ := enrollWorld(t)
	prof := demoProfile + `install:
  id: demo-argv
  summary: "装演示"
  where: "~/.demo"
  takes: "几秒"
  notes: ["演示出厂时不需要批准"]
  steps:
    - { kind: argv, argv: [/bin/echo, hi] }
`
	if err := os.WriteFile(filepath.Join(s.Dirs.Data, "profiles", "demo.yaml"), []byte(prof), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	plan, err := s.PlanInstall(ctx, "box", "demo")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Where != "~/.demo" || plan.Takes != "几秒" || len(plan.Notes) != 1 {
		t.Fatalf("%+v", plan)
	}
	if plan.Existing == nil || !strings.HasSuffix(plan.Existing.StateDir, ".demo") || plan.Existing.Enrolled != "" {
		t.Fatalf("existing %+v", plan.Existing)
	}
	if _, err := s.Enroll(ctx, "box", EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatal(err)
	}
	if plan, err = s.PlanInstall(ctx, "box", "demo"); err != nil || plan.Existing == nil || plan.Existing.Enrolled != "demo" {
		t.Fatalf("%+v %v", plan, err)
	}
}
