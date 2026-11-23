package service

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func TestRunInstallWithLocalPTY(t *testing.T) {
	body := []byte(cleanDemoScript)
	s, _, _ := installScriptWorld(t, body, sha256Hex(body))
	ctx := context.Background()

	plan, err := s.PlanInstall(ctx, "box", "demo")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	var mu sync.Mutex
	var events []bool
	tio := InstallIO{
		Stdin:  strings.NewReader(""),
		Stdout: &out,
		Stderr: &out,
		PTY:    true,
		OnResize: func(fn func(cols, rows, wpx, hpx int)) {
			mu.Lock()
			defer mu.Unlock()
			events = append(events, fn != nil)
			if fn != nil {
				fn(120, 40, 960, 640)
			}
		},
	}
	res, err := s.RunInstall(ctx, plan, tio)
	if err != nil {
		t.Fatalf("RunInstall PTY: %v\n%s", err, out.String())
	}
	if !res.Completed || res.Found == nil || res.Found.ProfileID != "demo" {
		t.Fatalf("unexpected result: %+v", res)
	}
	if !strings.Contains(out.String(), "installed args=--ver 1") {
		t.Fatalf("expected script output in PTY buffer, got %q", out.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(events) != 2 || !events[0] || events[1] {
		t.Fatalf("expected OnResize register then clear, got %v", events)
	}
}
