package service

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"valise/core/action"
	"valise/core/agentkit"
	"valise/core/enroll"
	"valise/core/profile"
	"valise/core/vaultrpc"
)

// Pure aggregation fault injection; RPC transport remains covered by the
// existing fake-ssh Health tests. A failed source must not erase other facts.
func TestHealthPartialSources(t *testing.T) {
	for _, failed := range []string{"backup", "service", "timer", "kit", "approval", "system", ""} {
		t.Run("failure_"+failed, func(t *testing.T) {
			s := newService(t, "")
			fault := func(id string) error {
				if id == failed {
					return errors.New("Authorization: Bearer synthetic-secret")
				}
				return nil
			}
			src := healthSources{
				vault: func() (*vaultrpc.StatusResult, error) { return &vaultrpc.StatusResult{}, fault("backup") },
				instances: func() ([]enroll.InstanceSummary, error) {
					return []enroll.InstanceSummary{{InstanceID: "demo"}}, fault("service")
				},
				timer: func() (*vaultrpc.TimerStatus, error) { return &vaultrpc.TimerStatus{}, fault("timer") },
				kit:   func() (*agentkit.KitStatusResult, error) { return &agentkit.KitStatusResult{}, fault("kit") },
				approval: func() (*action.ApprovalResult, error) {
					return &action.ApprovalResult{Declared: true, Mode: profile.ApprovalAsk}, fault("approval")
				},
				system: func() (*systemRead, error) {
					return &systemRead{status: &SystemStatus{Linger: "unknown"}}, fault("system")
				},
			}
			r := s.collectHealth("box", "demo", src)
			want := 0
			if failed != "" {
				want = 1
			}
			if r.Incomplete() != want {
				t.Fatalf("missing failures: %+v", r.Checks)
			}
			if len(r.Checks) != 6 {
				t.Fatalf("check disappeared: %+v", r.Checks)
			}
			for _, check := range r.Checks {
				if check.ID == failed && (check.State != "failed" || check.Reason == "") {
					t.Fatalf("unreported failure: %+v", check)
				}
			}
			if failed != "backup" {
				if b := healthItem(t, r, "backup"); b.Level != HealthBad {
					t.Fatal("known backup problem lost")
				}
			}
			healthItem(t, r, "restore_test")
			if failed != "timer" {
				healthItem(t, r, "timer")
			}
			b, _ := json.Marshal(r)
			if strings.Contains(string(b), "synthetic-secret") || strings.Contains(string(b), "Authorization") {
				t.Fatal("raw error leaked")
			}
		})
	}
}

func TestHealthUnsupportedAndUnknownApproval(t *testing.T) {
	s := newService(t, "")
	chk := &action.ApprovalResult{}
	src := healthSources{
		vault:     func() (*vaultrpc.StatusResult, error) { return &vaultrpc.StatusResult{}, nil },
		instances: func() ([]enroll.InstanceSummary, error) { return []enroll.InstanceSummary{{InstanceID: "demo"}}, nil },
		timer:     func() (*vaultrpc.TimerStatus, error) { return &vaultrpc.TimerStatus{}, nil },
		kit:       func() (*agentkit.KitStatusResult, error) { return &agentkit.KitStatusResult{}, nil },
		approval:  func() (*action.ApprovalResult, error) { return chk, nil },
	}
	r := s.collectHealth("box", "demo", src)
	if r.Incomplete() != 0 || r.Checks[4].State != "unsupported" {
		t.Fatalf("missing capability treated as failure: %+v", r.Checks)
	}
	chk = &action.ApprovalResult{Declared: true, Mode: profile.ApprovalUnknown, Detail: "synthetic-secret"}
	r = s.collectHealth("box", "demo", src)
	if r.Incomplete() != 1 || healthItem(t, r, "approval").Level == HealthOK {
		t.Fatalf("unknown approval passed: %+v", r)
	}
	b, _ := json.Marshal(r)
	if strings.Contains(string(b), "synthetic-secret") {
		t.Fatal("unknown-command output leaked")
	}
}
