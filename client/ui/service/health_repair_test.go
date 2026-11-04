package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"valise/core/enroll"
	"valise/core/valise"
)

const repairService = `service:
  candidates: [{scope: user, unit: demo.service}]
  repair_action: recover
`

func TestServiceRepairAction(t *testing.T) {
	old := actionSettle
	actionSettle = 0
	t.Cleanup(func() { actionSettle = old })
	for _, mode := range []string{"resolved", "remaining", "failed", "lost"} {
		t.Run(mode, func(t *testing.T) {
			s, home := enrollWorld(t)
			bin := filepath.Join(home, ".local/bin")
			status := filepath.Join(home, "service-active")
			os.WriteFile(status, []byte("inactive"), 0600)
			os.WriteFile(filepath.Join(bin, "systemctl"), []byte("#!/bin/sh\nprintf 'LoadState=loaded\\nActiveState='\ncat \"$HOME/service-active\"\nprintf '\\nUnitFileState=enabled\\n'\n"), 0755)
			body := "#!/bin/sh\n"
			switch mode {
			case "resolved":
				body += "printf active > \"$HOME/service-active\"\n"
			case "failed":
				body += "exit 7\n"
			case "lost":
				body += "mv \"$HOME/.local/bin/valise-porter\" \"$HOME/.local/bin/porter-disabled\"\n"
			default:
				body += "exit 0\n"
			}
			os.WriteFile(filepath.Join(bin, "demo-repair"), []byte(body), 0755)
			prof := demoProfile + repairService + `actions:
  - {id: recover, name: 重启服务, explain: 重启隔离服务, risk: manage, argv: ["${HOME}/.local/bin/demo-repair"], output: none}
`
			path := filepath.Join(s.Dirs.Data, "profiles/demo.yaml")
			os.WriteFile(path, []byte(prof), 0644)
			ctx := context.Background()
			if _, err := s.Enroll(ctx, "box", EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
				t.Fatal(err)
			}
			r, err := s.Health(ctx, "box", "demo")
			if err != nil {
				t.Fatal(err)
			}
			it := healthItem(t, r, "service")
			if it.ActionID != "recover" || it.Level != HealthBad {
				t.Fatalf("missing repair: %+v", it)
			}
			// Dismissal changes reminders only; the real problem is still actionable.
			if _, err := s.ToggleDismiss("box", "demo", it); err != nil {
				t.Fatal(err)
			}
			res, err := s.RunAction(ctx, "box", "demo", "recover", ActionOptions{})
			if err != nil {
				t.Fatal(err)
			}
			want := mode
			if mode == "failed" {
				want = "remaining"
			}
			if mode == "lost" {
				want = "unknown"
			}
			if res.Verification == nil || res.Verification.State != want {
				t.Fatalf("%+v", res)
			}
			if mode == "failed" && res.Exit != 7 {
				t.Fatalf("exit %d", res.Exit)
			}
			if mode != "lost" {
				audit, _ := os.ReadFile(filepath.Join(home, ".local/state/valise/audit.jsonl"))
				if !strings.Contains(string(audit), "recover") {
					t.Fatal("missing action audit")
				}
				os.WriteFile(path, []byte(strings.Replace(prof, "profile_version: 1", "profile_version: 2", 1)), 0644)
				_, err = s.RunAction(ctx, "box", "demo", "recover", ActionOptions{})
				if ue, ok := valise.As(err); !ok || ue.Code != "action_profile_stale" {
					t.Fatalf("stale ran: %v", err)
				}
			}
		})
	}
}

func TestServiceVerificationIgnoresDismissal(t *testing.T) {
	until := time.Now().Add(time.Hour)
	r := &HealthReport{Checks: []HealthCheck{{ID: "service", State: "ok"}}, Items: []HealthItem{{ID: "service", Level: HealthBad, Dismissed: &until}}}
	if verifyService(r, nil).State != "remaining" {
		t.Fatal("dismissal resolved problem")
	}
	r.Checks[0].State = "failed"
	if verifyService(r, nil).State != "unknown" {
		t.Fatal("failed check resolved problem")
	}
	if verifyService(nil, errors.New("synthetic-secret")).State != "unknown" {
		t.Fatal("failed RPC resolved problem")
	}
	s, _ := enrollWorld(t)
	it := HealthItem{Level: HealthBad}
	s.bindServiceRepair(&it, enroll.InstanceSummary{ProfileID: "demo", Name: "unverified"})
	if it.ActionID != "" {
		t.Fatal("invented action for unverified named instance")
	}
}
