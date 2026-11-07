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
 touchprog) printf x > "$EXAMPLE_STATE_DIR/skills/program/cache.json";;
 capture) rm -rf "$EXAMPLE_STATE_DIR/tmp/cap"; mkdir -p "$EXAMPLE_STATE_DIR/tmp/cap"; ln -s "$HOME/prefix/lib/node_modules/demo/skill" "$EXAMPLE_STATE_DIR/tmp/cap/program";;
 livelink) ln -s "$HOME/live/inner" "$EXAMPLE_STATE_DIR/tmp/live";;
 stagelink) ln -s "$EXAMPLE_STATE_DIR/config.json" "$EXAMPLE_STATE_DIR/tmp/cap/stage";;
esac
if test "$1" = status && test -e "$HOME/capture-on-read"; then "$0" capture >/dev/null; fi
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
			if verb == "outside" && !strings.Contains(j.Failure, "memory.txt") {
				t.Fatalf("undeclared path not named: %q", j.Failure)
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

// linkFixture adds an absolute link inside the state dir and a link into
// the recorded npm package (docs/INSTALL.md 5.2.1).
func linkFixture(t *testing.T) (Runner, *profile.Profile, enroll.Record, string) {
	t.Helper()
	r, p, rec := fixture(t, "active")
	live := rec.Roots["state"]
	prefix := filepath.Join(r.Env.Home, "prefix")
	pkg := filepath.Join(prefix, "lib", "node_modules", "demo", "skill")
	for _, d := range []string{pkg, filepath.Join(live, "skills"), filepath.Join(live, "inner")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(pkg, "SKILL.md"), []byte("program skill"), 0o644)
	os.WriteFile(filepath.Join(live, "inner", "SKILL.md"), []byte("inner skill"), 0o644)
	os.Symlink(filepath.Join(live, "inner"), filepath.Join(live, "skills", "inner"))
	os.Symlink(pkg, filepath.Join(live, "skills", "program"))
	rec.Provenance = enroll.Provenance{Kind: "npm_global", NPM: &enroll.NPMRecord{Package: "demo", Version: "1.2.3", Prefix: prefix}}
	if _, err := porterenroll.WriteRecord(r.Env.Home, rec, *p); err != nil {
		t.Fatal(err)
	}
	return r, p, rec, pkg
}

func TestStagedLinksPublishAndRestore(t *testing.T) {
	r, _, rec, pkg := linkFixture(t)
	plan, err := Plan(context.Background(), r.Env, "demo", "approval-ask")
	if err != nil || len(plan.Links) != 2 || plan.LinkBytes != int64(len("program skill")) {
		t.Fatalf("%+v %v", plan, err)
	}
	// A client that cannot show link handling is refused before stopping anything.
	if _, err := r.Run(context.Background(), action.RunParams{InstanceID: "demo", ActionID: "approval-ask", PlanDigest: plan.Digest}); err == nil {
		t.Fatal("old client accepted")
	}
	if calls, _ := os.ReadFile(filepath.Join(r.Env.Home, "calls")); len(calls) != 0 {
		t.Fatalf("service touched: %s", calls)
	}
	res, err := r.Run(context.Background(), action.RunParams{InstanceID: "demo", ActionID: "approval-ask", PlanDigest: plan.Digest, LinkHandling: action.LinkHandlingVersion})
	if err != nil || !res.Operation.Published || res.Operation.Approval != "resolved" {
		t.Fatalf("%+v %v", res, err)
	}
	live := rec.Roots["state"]
	for rel, want := range map[string]string{"skills/inner": filepath.Join(live, "inner"), "skills/program": pkg} {
		if got, _ := os.Readlink(filepath.Join(live, rel)); got != want {
			t.Fatalf("%s = %q, want original %q", rel, got, want)
		}
	}
}

func TestProgramCopyWriteNeverPublishes(t *testing.T) {
	r, p, rec, pkg := linkFixture(t)
	p.Actions[0].Steps = append(p.Actions[0].Steps, profile.ActionStep{ID: "touch", Argv: profile.Argv{"example", "touchprog"}})
	if _, err := porterenroll.WriteRecord(r.Env.Home, rec, *p); err != nil {
		t.Fatal(err)
	}
	plan, err := Plan(context.Background(), r.Env, "demo", "approval-ask")
	if err != nil {
		t.Fatal(err)
	}
	res, err := r.Run(context.Background(), action.RunParams{InstanceID: "demo", ActionID: "approval-ask", PlanDigest: plan.Digest, LinkHandling: action.LinkHandlingVersion})
	if err != nil || res.Operation.Published || !strings.Contains(res.Operation.Failure, "程序代码") || res.Operation.Services != "restored" {
		t.Fatalf("%+v %v", res.Operation, err)
	}
	if _, err := os.Stat(filepath.Join(pkg, "cache.json")); !os.IsNotExist(err) {
		t.Fatal("the installed program was written")
	}
	b, _ := os.ReadFile(filepath.Join(rec.Roots["state"], "config.json"))
	if !strings.Contains(string(b), "full") {
		t.Fatal("failed stage published")
	}
}

// The confirmation binds the action and the link rules, not the readings of
// a state dir the running instance keeps writing (INSTALL.md 5.2.1): links
// that appear after confirming are read after the stop and handled; a link
// that cannot be handled is refused before anything stops.
func TestLinkReadingsAfterConfirmation(t *testing.T) {
	r, _, rec, pkg := linkFixture(t)
	plan, err := Plan(context.Background(), r.Env, "demo", "approval-ask")
	if err != nil {
		t.Fatal(err)
	}
	live := rec.Roots["state"]
	os.WriteFile(filepath.Join(live, "inner", "grown.log"), []byte("written after confirming"), 0o600)
	os.MkdirAll(filepath.Join(live, "tmp"), 0o755)
	os.Symlink(filepath.Join(live, "inner"), filepath.Join(live, "tmp", "new-inner"))
	res, err := r.Run(context.Background(), action.RunParams{InstanceID: "demo", ActionID: "approval-ask", PlanDigest: plan.Digest, LinkHandling: action.LinkHandlingVersion})
	if err != nil || !res.Operation.Published || len(res.Operation.Plan.Links) != 3 {
		t.Fatalf("%+v %v", res.Operation, err)
	}
	if got, _ := os.Readlink(filepath.Join(live, "tmp", "new-inner")); got != filepath.Join(live, "inner") {
		t.Fatalf("new link not restored: %q", got)
	}
	_ = pkg

	r2, _, rec2, _ := linkFixture(t)
	plan, err = Plan(context.Background(), r2.Env, "demo", "approval-ask")
	if err != nil {
		t.Fatal(err)
	}
	elsewhere := t.TempDir()
	os.Symlink(elsewhere, filepath.Join(rec2.Roots["state"], "skills", "elsewhere"))
	res, err = r2.Run(context.Background(), action.RunParams{InstanceID: "demo", ActionID: "approval-ask", PlanDigest: plan.Digest, LinkHandling: action.LinkHandlingVersion})
	// Found when the plan is recomputed at the start, before anything stops.
	if err == nil || !strings.Contains(err.Error(), "skills/elsewhere") {
		t.Fatalf("%+v %v", res, err)
	}
	if calls, _ := os.ReadFile(filepath.Join(r2.Env.Home, "calls")); len(calls) != 0 {
		t.Fatalf("service touched: %s", calls)
	}
}

// scratchFixture: a scratch area the harness rebuilds on every CLI call,
// holding staged links the command removes (INSTALL.md 5.2.1).
func scratchFixture(t *testing.T, extra ...string) (Runner, enroll.Record, string) {
	t.Helper()
	r, p, rec, pkg := linkFixture(t)
	live := rec.Roots["state"]
	os.MkdirAll(filepath.Join(live, "tmp", "cap"), 0o755)
	os.Symlink(pkg, filepath.Join(live, "tmp", "cap", "old"))
	os.Symlink(filepath.Join(live, "inner"), filepath.Join(live, "tmp", "cap", "in"))
	os.WriteFile(filepath.Join(r.Env.Home, "capture-on-read"), nil, 0o600)
	a := &p.Actions[0]
	a.Transaction.Scratch = []string{"tmp"}
	for _, v := range append([]string{"capture"}, extra...) {
		a.Steps = append(a.Steps, profile.ActionStep{ID: v, Argv: profile.Argv{"example", v}})
	}
	if _, err := porterenroll.WriteRecord(r.Env.Home, rec, *p); err != nil {
		t.Fatal(err)
	}
	return r, rec, pkg
}

func runLinks(t *testing.T, r Runner) *action.Operation {
	t.Helper()
	plan, err := Plan(context.Background(), r.Env, "demo", "approval-ask")
	if err != nil || len(plan.Links) != 4 {
		t.Fatalf("%+v %v", plan, err)
	}
	res, err := r.Run(context.Background(), action.RunParams{InstanceID: "demo", ActionID: "approval-ask", PlanDigest: plan.Digest, LinkHandling: action.LinkHandlingVersion})
	if err != nil {
		t.Fatal(err)
	}
	return res.Operation
}

func TestScratchLinksPublish(t *testing.T) {
	r, rec, pkg := scratchFixture(t, "livelink")
	op := runLinks(t, r)
	if !op.Published || op.Services != "restored" || len(op.WriteLinks) != 2 {
		t.Fatalf("%+v", op)
	}
	live := rec.Roots["state"]
	for rel, want := range map[string]string{"skills/program": pkg, "tmp/cap/program": pkg, "tmp/live": filepath.Join(r.Env.Home, "live", "inner")} {
		if got, _ := os.Readlink(filepath.Join(live, rel)); got != want {
			t.Fatalf("%s = %q, want %q", rel, got, want)
		}
	}
	if _, err := os.Lstat(filepath.Join(live, "tmp", "cap", "old")); !os.IsNotExist(err) {
		t.Fatal("a link the command removed was put back")
	}
	if b, _ := os.ReadFile(filepath.Join(live, "tmp", "live", "SKILL.md")); string(b) != "inner skill" {
		t.Fatalf("live link does not resolve after the swap: %q", b)
	}
}

func TestScratchLinkIntoStageRefused(t *testing.T) {
	r, rec, _ := scratchFixture(t, "stagelink")
	op := runLinks(t, r)
	if op.Published || op.Services != "restored" || !strings.Contains(op.Failure, "不能放行的链接") {
		t.Fatalf("%+v", op)
	}
	if b, _ := os.ReadFile(filepath.Join(rec.Roots["state"], "config.json")); !strings.Contains(string(b), "full") {
		t.Fatal("refused stage published")
	}
}

func TestScratchOverlapRejectedByLint(t *testing.T) {
	_, p, _ := fixture(t, "active")
	p.Actions[0].Transaction.Scratch = []string{"config.json"}
	issues := profile.ValidateTransaction(p.Actions[0], p.ApprovalCheck)
	if len(issues) == 0 || !strings.Contains(issues[0].Field, "scratch") {
		t.Fatalf("%+v", issues)
	}
}
