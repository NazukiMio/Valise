package actiontxn

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"valise/core/action"
	"valise/core/profile"
	porterenroll "valise/porter/internal/enroll"
)

func TestServiceDropInChangeInvalidatesConfirmation(t *testing.T) {
	r, _, _ := fixture(t, "active")
	dropin := filepath.Join(r.Env.Home, "dropin")
	if err := os.WriteFile(dropin, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	plan, err := Plan(context.Background(), r.Env, "demo", "approval-ask")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(dropin, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Run(context.Background(), action.RunParams{InstanceID: "demo", ActionID: "approval-ask", PlanDigest: plan.Digest}); err == nil {
		t.Fatal("changed drop-in accepted")
	}
	if _, err = os.Stat(filepath.Join(r.Env.Home, "calls")); !os.IsNotExist(err) {
		t.Fatal("changed identity stopped service")
	}
}

func TestMultipleServicesRestoreAcrossPartialFailures(t *testing.T) {
	for _, failure := range []string{"stop", "start"} {
		t.Run(failure, func(t *testing.T) {
			r, p, rec := fixture(t, "active")
			p.Service.Candidates = append(p.Service.Candidates, profile.ServiceUnit{Scope: "user", Unit: "second.service"})
			for name, body := range map[string]string{"second.service": "active", "example.service": "active", "fail-" + failure: ""} {
				if err := os.WriteFile(filepath.Join(r.Env.Home, name), []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			script := `#!/bin/sh
verb="$2"; unit="$3"
case "$verb" in
 show) printf 'Id=%s\nLoadState=loaded\nFragmentPath=%s/unit\nActiveState=' "$unit" "$HOME"; cat "$HOME/$unit"; printf '\n';;
 stop) printf 'stop %s\n' "$unit" >> "$HOME/calls"; if test "$unit" = second.service && test -e "$HOME/fail-stop"; then exit 1; fi; printf inactive > "$HOME/$unit";;
 start) printf 'start %s\n' "$unit" >> "$HOME/calls"; if test "$unit" = second.service && test -e "$HOME/fail-start"; then printf failed > "$HOME/$unit"; exit 1; fi; printf active > "$HOME/$unit";;
 reset-failed) printf inactive > "$HOME/$unit";;
esac
`
			if err := os.WriteFile(filepath.Join(r.Env.Home, "bin/systemctl"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			if _, err := porterenroll.WriteRecord(r.Env.Home, rec, *p); err != nil {
				t.Fatal(err)
			}
			res := execute(t, r)
			j := res.Operation
			if res.Exit == 0 {
				t.Fatal("failure ignored")
			}
			if failure == "stop" {
				if j.Published || len(j.Steps) != 0 || j.Services != "restored" {
					t.Fatal(j)
				}
			} else {
				if !j.Published || j.Done {
					t.Fatal(j)
				}
				if err := os.Remove(filepath.Join(r.Env.Home, "fail-start")); err != nil {
					t.Fatal(err)
				}
				resumed, err := r.Resume(context.Background(), action.ResumeParams{OperationID: j.ID, Choice: action.ResumeRestore})
				if err != nil || resumed.Exit != 0 {
					t.Fatal(resumed, err)
				}
			}
			for _, unit := range []string{"example.service", "second.service"} {
				state, _ := os.ReadFile(filepath.Join(r.Env.Home, unit))
				if string(state) != "active" {
					t.Fatal(unit, string(state))
				}
			}
			calls, _ := os.ReadFile(filepath.Join(r.Env.Home, "calls"))
			if strings.Count(string(calls), "start example.service\n") != 1 {
				t.Fatal("already restored service restarted", string(calls))
			}
		})
	}
}
