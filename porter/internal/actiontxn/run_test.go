package actiontxn

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"valise/core/action"
	"valise/core/enroll"
	"valise/core/profile"
	"valise/porter/internal/actionjournal"
	porterenroll "valise/porter/internal/enroll"
)

func fixture(t *testing.T, active string) (Runner, *profile.Profile, enroll.Record) {
	t.Helper()
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	live := filepath.Join(home, "live")
	for _, d := range []string{bin, live} {
		if err := os.Mkdir(d, 0700); err != nil {
			t.Fatal(err)
		}
	}
	put := func(name, body string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(home, name), []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	put("unit", "fixture unit", 0600)
	put("active", active, 0600)
	put("live/config.json", `{"security":"full","ask":"off"}`, 0600)
	put("live/memory.txt", "original memory", 0600)
	put("bin/example", `#!/bin/sh
case "$1" in
 --version) printf '1.2.3\n';;
 status) cat "$EXAMPLE_STATE_DIR/config.json";;
 configure) printf '{"security":"allowlist","ask":"on-miss"}' > "$EXAMPLE_STATE_DIR/config.json";;
 fail) printf 'do-not-persist-this-output\n'; exit 7;;
 wrong) printf '{"security":"allowlist","ask":"always"}' > "$EXAMPLE_STATE_DIR/config.json";;
 outside) printf changed > "$EXAMPLE_STATE_DIR/memory.txt";;
esac
`, 0700)
	put("bin/systemctl", `#!/bin/sh
verb="$2"
case "$verb" in
 show)
 printf 'Id=example.service\nLoadState=loaded\nFragmentPath=%s/unit\nActiveState=' "$HOME"
 cat "$HOME/active"; printf '\n'; if test -e "$HOME/dropin"; then printf 'DropInPaths=%s/dropin\n' "$HOME"; fi;;
 stop) printf 'stop\n' >> "$HOME/calls"; printf inactive > "$HOME/active";;
 start) printf 'start\n' >> "$HOME/calls"; if test -e "$HOME/fail-start"; then printf failed > "$HOME/active"; exit 1; fi; printf active > "$HOME/active";;
 reset-failed) printf 'reset-failed\n' >> "$HOME/calls"; printf inactive > "$HOME/active";;
esac
`, 0700)
	p := &profile.Profile{Schema: profile.Schema, ID: "demo", ProfileVersion: 1, DisplayName: "Demo", Detect: &profile.Detect{VersionArgv: profile.Argv{"example", "--version"}, BinDirs: []string{"${HOME}/bin"}}, Service: &profile.Service{Candidates: []profile.ServiceUnit{{Scope: "user", Unit: "example.service"}}}, ApprovalCheck: &profile.ApprovalCheck{Argv: profile.Argv{"example", "status"}, BypassWhen: map[string]string{"security": "full"}, RepairAction: "approval-ask", RepairExpect: map[string]string{"security": "allowlist", "ask": "on-miss"}}, Actions: []profile.Action{{ID: "approval-ask", Name: "改为执行前询问", Explain: "白名单外询问", Undo: "恢复备份", Risk: profile.RiskConfig, Output: profile.OutputNone, Steps: []profile.ActionStep{{ID: "configure", Argv: profile.Argv{"example", "configure"}}}, Transaction: &profile.ActionTransaction{Root: "state", Env: map[string]string{"EXAMPLE_STATE_DIR": "${STAGE_DIR}"}, Writes: []string{"config.json"}}, Verification: &profile.ActionVerification{Versions: []string{"1.2.3"}, Env: "fixture", Date: "2026-11-05"}}}}
	rec := enroll.Record{InstanceID: "demo", ProfileID: p.ID, ProfileVersion: p.ProfileVersion, Roots: map[string]string{"state": live}, Env: map[string]string{"EXAMPLE_STATE_DIR": live}}
	if _, err := porterenroll.WriteRecord(home, rec, *p); err != nil {
		t.Fatal(err)
	}
	return Runner{Env: &porterenroll.Env{Home: home, Vars: []string{"HOME=" + home, "PATH=" + bin + ":/usr/bin:/bin"}}, Idle: func([]string) bool { return true }}, p, rec
}
func execute(t *testing.T, r Runner) *action.RunResult {
	t.Helper()
	plan, err := Plan(context.Background(), r.Env, "demo", "approval-ask")
	if err != nil {
		t.Fatal(err)
	}
	res, err := r.Run(context.Background(), action.RunParams{InstanceID: "demo", ActionID: "approval-ask", PlanDigest: plan.Digest})
	if err != nil {
		t.Fatal(err)
	}
	return res
}
func TestOriginalServiceStateAndPublishedConfiguration(t *testing.T) {
	for _, original := range []string{"active", "inactive"} {
		t.Run(original, func(t *testing.T) {
			r, _, rec := fixture(t, original)
			res := execute(t, r)
			if res.Exit != 0 || res.Operation == nil || !res.Operation.Published || res.Operation.Approval != "resolved" || res.Operation.Services != "restored" {
				t.Fatalf("%+v", res)
			}
			b, err := os.ReadFile(filepath.Join(rec.Roots["state"], "config.json"))
			if err != nil || !strings.Contains(string(b), "allowlist") {
				t.Fatal(string(b), err)
			}
			state, _ := os.ReadFile(filepath.Join(r.Env.Home, "active"))
			if string(state) != original {
				t.Fatal("original state lost", string(state))
			}
			calls, _ := os.ReadFile(filepath.Join(r.Env.Home, "calls"))
			want := "stop\nstart\n"
			if original == "inactive" {
				want = ""
			}
			if string(calls) != want {
				t.Fatal("unexpected service calls", string(calls))
			}
			old, _ := os.ReadFile(filepath.Join(res.Operation.Previous, "config.json"))
			if !strings.Contains(string(old), "full") {
				t.Fatal("original configuration not retained")
			}
		})
	}
}
func TestStepAndApprovalFailuresNeverPublish(t *testing.T) {
	for _, verb := range []string{"fail", "wrong", "outside"} {
		t.Run(verb, func(t *testing.T) {
			r, p, rec := fixture(t, "active")
			p.Actions[0].Steps = append(p.Actions[0].Steps, profile.ActionStep{ID: "second", Argv: profile.Argv{"example", verb}})
			if verb == "fail" {
				p.Actions[0].Steps = append(p.Actions[0].Steps, profile.ActionStep{ID: "never", Argv: profile.Argv{"example", "configure"}})
			}
			if _, err := porterenroll.WriteRecord(r.Env.Home, rec, *p); err != nil {
				t.Fatal(err)
			}
			res := execute(t, r)
			j := res.Operation
			if res.Exit == 0 || j.Published || j.Failure == "" || j.Services != "restored" {
				t.Fatalf("%+v", j)
			}
			if verb == "fail" && len(j.Steps) != 2 {
				t.Fatal("steps continued after first error")
			}
			live, _ := os.ReadFile(filepath.Join(rec.Roots["state"], "config.json"))
			if !strings.Contains(string(live), "full") {
				t.Fatal("failed stage published")
			}
			// The third configure step would hide wrong-policy changes; keep wrong last.
			if verb == "fail" {
				data, err := os.ReadFile(filepath.Join((actionjournal.Store{Home: r.Env.Home}).Dir(), "done", j.ID+".json"))
				if err != nil || strings.Contains(string(data), "do-not-persist") {
					t.Fatal("subprocess output persisted", err)
				}
			}
		})
	}
}
