package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"valise/core/identity"
	"valise/core/protection"
)

func TestEnrollBatchEnrollsAndReportsFailures(t *testing.T) {
	s, home := enrollWorld(t)
	ctx := context.Background()
	res, err := s.EnrollBatch(ctx, "box", []EnrollBatchItem{
		{ProfileID: "demo", Assign: []string{"state:notes/**=memory"}},
		{ProfileID: "nope"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Enrolled) != 1 || res.Enrolled[0] != "demo" || len(res.Failures) != 1 || res.Failures[0].Reason == "" || res.Failures[0].Err == nil {
		t.Fatalf("%+v", res)
	}
	if _, err := os.Stat(filepath.Join(home, ".config/valise/dossiers/demo.json")); err != nil {
		t.Fatal(err)
	}
}

func TestQuickReenrollUnknown(t *testing.T) {
	s, _ := enrollWorld(t)
	_, err := s.QuickReenroll(context.Background(), "box", "ghost")
	wantCode(t, err, "instance_unknown")
	if _, err := s.EnrollBatch(context.Background(), "box", []EnrollBatchItem{{ProfileID: "demo"}}); err != nil {
		t.Fatal(err)
	}
	res, err := s.QuickReenroll(context.Background(), "box", "demo")
	if err != nil || len(res.Enrolled) != 1 || len(res.Failures) != 0 {
		t.Fatalf("%v %+v", err, res)
	}
}

func TestEnrollBatchReactivatesSupersededPlan(t *testing.T) {
	s, home := enrollWorld(t)
	writeExe := func(rel, body string) {
		p := filepath.Join(home, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeExe(".local/bin/systemctl", `#!/bin/sh
shift
if test "$1" = '--timestamp=unix'; then shift; fi
case "$1" in
 show)
  if test -e "$HOME/.config/systemd/user/$2"; then
   if test -e "$HOME/.config/systemd/user/$2.enabled"; then
    printf 'LoadState=loaded\nActiveState=active\nUnitFileState=enabled\nNextElapseUSecRealtime=@1793934000\nLastTriggerUSec=@1793847600\n'
   else
    printf 'LoadState=loaded\nActiveState=inactive\nUnitFileState=disabled\nNextElapseUSecRealtime=n/a\nLastTriggerUSec=n/a\n'
   fi
  else printf 'LoadState=not-found\n'; fi ;;
 enable)
  for a in "$@"; do
   case "$a" in *.timer) touch "$HOME/.config/systemd/user/$a.enabled" ;; esac
  done
  exit 0 ;;
 disable)
  for a in "$@"; do
   case "$a" in *.timer) rm "$HOME/.config/systemd/user/$a.enabled" 2>/dev/null || true ;; esac
  done
  exit 0 ;;
 daemon-reload|reset-failed|start|stop) exit 0 ;;
 is-enabled|is-active) exit 1 ;;
 *) exit 0 ;;
esac
`)
	writeExe(".local/bin/systemd-analyze", "#!/bin/sh\nprintf '  Normalized form: %s\\n  Next elapse: Mon 2026-11-30 03:30:00 UTC\\n' \"$2\"\n")
	writeExe(".local/bin/loginctl", "#!/bin/sh\nprintf 'yes\\n'\n")
	ctx := context.Background()
	if _, err := s.EnrollBatch(ctx, "box", []EnrollBatchItem{{ProfileID: "demo"}}); err != nil {
		t.Fatal(err)
	}
	var b1 identity.Binding
	if err := s.call(ctx, "box", identity.MethodGet, identity.GetParams{InstanceID: "demo"}, &b1); err != nil || b1.Agent == nil {
		t.Fatalf("binding1: %+v %v", b1, err)
	}
	off := false
	custom := protection.Desired{
		Scope:        protection.Scope{Kind: "preset", Tier: "soul"},
		Schedule:     protection.Schedule{Enabled: &off, Cadence: "daily", Timezone: "UTC"},
		Period:       protection.Period{Unit: "month", Timezone: "UTC"},
		ServerCopies: []protection.ServerCopy{},
	}
	if ok, note := s.carryOverProtection(ctx, "box", b1.Agent.Ref, "seed-soul", custom); !ok {
		t.Fatalf("seed protection: %s", note)
	}
	// Remove active identity/instance record while leaving protection/demo.json so next enroll has a new generation and sees SupersededPlan.
	_ = os.Remove(filepath.Join(home, ".config/valise/identity/agents/demo.json"))
	_ = os.Remove(filepath.Join(home, ".config/valise/dossiers/demo.json"))
	_ = os.Remove(filepath.Join(home, ".config/valise/instances/demo.json"))

	res, err := s.EnrollBatch(ctx, "box", []EnrollBatchItem{{ProfileID: "demo", EnableDefaultPlan: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Enrolled) != 1 || len(res.PlanApplied) != 1 || res.PlanApplied[0] != "demo" {
		t.Fatalf("expected superseded plan reactivated, got %+v", res)
	}
	var b2 identity.Binding
	if err := s.call(ctx, "box", identity.MethodGet, identity.GetParams{InstanceID: "demo"}, &b2); err != nil || b2.Agent == nil {
		t.Fatalf("binding2: %+v %v", b2, err)
	}
	view, err := s.GetProtection(ctx, "box", protection.GetParams{AgentRef: b2.Agent.Ref})
	if err != nil || view.Mode != "accepted" || view.ServerPlan == nil || view.ServerPlan.Desired.Scope.Tier != "soul" {
		t.Fatalf("expected reactivated soul plan, got %+v %v", view, err)
	}
}
