package timer

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"valise/core/identity"
	cp "valise/core/protection"
	"valise/core/vaultrpc"
	porterenroll "valise/porter/internal/enroll"
)

func timerPlan(t *testing.T) (string, *porterenroll.Env, identity.AgentRef, cp.ServerPlan) {
	t.Helper()
	home := t.TempDir()
	a := identity.AgentRef{ServerUID: "server", InstallID: "install", InstanceID: "agent", Generation: "generation"}
	d := cp.NewDefault()
	yes := true
	d.Schedule.Enabled = &yes
	d.Schedule.DailyAt = "03:00"
	d.Schedule.Timezone = "UTC"
	d.Period = cp.Period{Unit: "month", Timezone: "Asia/Shanghai"}
	p := cp.ServerPlan{SchemaVersion: cp.Schema, AgentRef: a, Revision: 7, Desired: d, AcceptedAt: time.Now().UTC()}
	timerWrite(t, filepath.Join(home, ".config", "valise", "protection", "agent.json"), p)
	timerWrite(t, filepath.Join(home, ".config", "valise", "instances", "agent.vault.json"), map[string]string{"unit": "month", "tz": "Asia/Shanghai"})
	return home, &porterenroll.Env{Home: home}, a, p
}
func timerWrite(t *testing.T, path string, value any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
}
func TestAcceptedTimerUsesPlanAndNeverLegacyFallback(t *testing.T) {
	home, e, a, p := timerPlan(t)
	calls := 0
	ctx := context.Background()
	validate := func(_ context.Context, ref identity.AgentRef) error {
		if ref != a {
			t.Fatal(ref)
		}
		return nil
	}
	run := func(_ context.Context, plan *cp.ServerPlan) error {
		calls++
		if plan.Revision != 7 || plan.Scope.Tier != "full" || plan.Period.Timezone != "Asia/Shanghai" {
			t.Fatal(plan)
		}
		return nil
	}
	if err := SealAccepted(ctx, e, home, "agent", validate, run); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal(calls)
	}
	if err := Seal(ctx, e, home, "agent"); err == nil {
		t.Fatal("legacy timer ran on adopted plan")
	}
	if _, err := Set(ctx, e, home, vaultrpc.TimerSetParams{InstanceID: "agent", Enabled: false}); err == nil {
		t.Fatal("legacy TimerSet accepted adopted instance")
	}
	no := false
	p.Schedule.Enabled = &no
	timerWrite(t, filepath.Join(home, ".config", "valise", "protection", "agent.json"), p)
	if err := SealAccepted(ctx, e, home, "agent", validate, run); err == nil {
		t.Fatal("disabled schedule ran")
	}
	if calls != 1 {
		t.Fatal("disabled schedule invoked runner")
	}
}
func TestAcceptedTimerCorruptOrMissingNeverFallsBack(t *testing.T) {
	home, e, a, _ := timerPlan(t)
	ctx := context.Background()
	validate := func(context.Context, identity.AgentRef) error { return nil }
	ran := false
	run := func(context.Context, *cp.ServerPlan) error { ran = true; return nil }
	timerWrite(t, filepath.Join(home, ".local", "state", "valise", "protection-changes", "bad.json"), map[string]string{"state": "pending"})
	if err := SealAccepted(ctx, e, home, a.InstanceID, validate, run); err == nil {
		t.Fatal("corrupt transaction allowed timer")
	}
	if ran {
		t.Fatal("corrupt timer ran")
	}
	other := t.TempDir()
	if err := SealAccepted(ctx, &porterenroll.Env{Home: other}, other, "agent", validate, run); err == nil {
		t.Fatal("missing plan fell back")
	}
}
