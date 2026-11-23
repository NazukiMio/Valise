package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"valise/core/install"
	"valise/testbed/fakesshenv"
)

func installScriptWorld(t *testing.T, scriptBody []byte, recordedSHA string) (*Service, string, *httptest.Server) {
	t.Helper()
	e := fakesshenv.New(t)
	home := e.AddHost("box")
	e.InstallPorter("box")

	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect-http" {
			http.Redirect(w, r, "http://127.0.0.1/bad.sh", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write(scriptBody)
	}))
	t.Cleanup(ts.Close)

	s := newService(t, e.SSH)
	s.Release.HTTP = ts.Client()
	profDir := filepath.Join(s.Dirs.Data, "profiles")
	if err := os.MkdirAll(profDir, 0o755); err != nil {
		t.Fatal(err)
	}
	prof := demoProfile + `install:
  id: demo-script
  summary: "安装演示脚本"
  where: "~/.demo"
  takes: "几秒"
  steps:
    - kind: script
      url: "` + ts.URL + `/install.sh"
      sha256: "` + recordedSHA + `"
      interpreter: [bash]
      args: [--ver, "1"]
      runtime_downloads: ""
`
	if err := os.WriteFile(filepath.Join(profDir, "demo.yaml"), []byte(prof), 0o644); err != nil {
		t.Fatal(err)
	}
	return s, home, ts
}

const cleanDemoScript = `#!/bin/sh
mkdir -p "$HOME/.demo" "$HOME/.local/bin"
printf '{}' > "$HOME/.demo/demo.json"
printf '#!/bin/sh\necho demo 1.0\n' > "$HOME/.local/bin/demo"
chmod +x "$HOME/.local/bin/demo"
echo "installed args=$*"
`

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestPlanAndRunInstallHashMatch(t *testing.T) {
	body := []byte(cleanDemoScript)
	s, home, _ := installScriptWorld(t, body, sha256Hex(body))
	ctx := context.Background()

	plan, err := s.PlanInstall(ctx, "box", "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 1 || plan.Steps[0].ScriptChanged || plan.Steps[0].ScriptRefused {
		t.Fatalf("unexpected step flags: %+v", plan.Steps)
	}
	if plan.Steps[0].SHA256 != sha256Hex(body) {
		t.Fatalf("sha mismatch: %s", plan.Steps[0].SHA256)
	}

	var out bytes.Buffer
	res, err := s.RunInstall(ctx, plan, InstallIO{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &out})
	if err != nil {
		t.Fatalf("RunInstall: %v\n%s", err, out.String())
	}
	if !res.Completed || res.Found == nil || res.Found.ProfileID != "demo" {
		t.Fatalf("expected detected demo after install: %+v", res)
	}
	if !strings.Contains(out.String(), "installed args=--ver 1") {
		t.Fatalf("script output missing args: %q", out.String())
	}
	staged := filepath.Join(home, ".local/state/valise/install", plan.ID, "install.sh")
	if _, err := os.Stat(staged); err != nil {
		t.Fatalf("staged script not uploaded: %v", err)
	}
}

func TestPlanAndRunInstallHashUpdatedConfirmAndRun(t *testing.T) {
	// Updated upstream script differs from profile's recorded SHA-256 and
	// contains a non-refused download URL finding.
	updated := []byte(cleanDemoScript + "# note\necho fetching https://cdn.example.invalid/pkg.tgz\n")
	oldSHA := strings.Repeat("0", 64)
	s, _, _ := installScriptWorld(t, updated, oldSHA)
	ctx := context.Background()

	plan, err := s.PlanInstall(ctx, "box", "demo")
	if err != nil {
		t.Fatalf("PlanInstall should allow review instead of deadlocking: %v", err)
	}
	st := plan.Steps[0]
	if !st.ScriptChanged || st.ScriptRefused || st.ExpectedSHA256 != oldSHA || st.SHA256 != sha256Hex(updated) {
		t.Fatalf("unexpected changed script metadata: %+v", st)
	}
	if len(st.ScriptFindings) == 0 || st.ScriptFindings[0].Rule != install.RuleDownload {
		t.Fatalf("expected download finding, got %+v", st.ScriptFindings)
	}

	// Must refuse to run before explicit confirmation bound to actual SHA-256.
	var out bytes.Buffer
	if _, err := s.RunInstall(ctx, plan, InstallIO{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &out}); err == nil {
		t.Fatal("expected unconfirmed updated script to be rejected")
	} else {
		wantCode(t, err, "install_script_changed")
	}

	// Tampering with AcceptedSHA256 must also fail.
	plan.Steps[0].ScriptAccepted = true
	plan.Steps[0].AcceptedSHA256 = oldSHA
	if _, err := s.RunInstall(ctx, plan, InstallIO{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &out}); err == nil {
		t.Fatal("expected mismatched AcceptedSHA256 to be rejected")
	}

	// Binding confirmation to actual SHA-256 proceeds cleanly.
	plan.AcceptScript(0, true)
	res, err := s.RunInstall(ctx, plan, InstallIO{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &out})
	if err != nil {
		t.Fatalf("RunInstall after confirmation: %v\n%s", err, out.String())
	}
	if !res.Completed || res.Found == nil || res.Found.ProfileID != "demo" {
		t.Fatalf("expected demo detected: %+v", res)
	}
}

func TestPlanAndRunInstallDangerousScriptRefused(t *testing.T) {
	evil := []byte("#!/bin/sh\nrm -rf /\n")
	oldSHA := strings.Repeat("1", 64)
	s, _, _ := installScriptWorld(t, evil, oldSHA)
	ctx := context.Background()

	plan, err := s.PlanInstall(ctx, "box", "demo")
	if err != nil {
		t.Fatalf("PlanInstall: %v", err)
	}
	if !plan.HasRefusedScript() || !plan.Steps[0].ScriptRefused {
		t.Fatalf("expected dangerous updated script to be marked refused: %+v", plan.Steps[0])
	}
	plan.AcceptScript(0, true)
	if plan.Steps[0].ScriptAccepted {
		t.Fatal("AcceptScript must not accept a refused script")
	}
	var out bytes.Buffer
	if _, err := s.RunInstall(ctx, plan, InstallIO{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &out}); err == nil {
		t.Fatal("expected refused script to be blocked")
	} else {
		wantCode(t, err, "install_script_refused")
	}
}

func TestCustomInstallCurlPipeBashClientDownloadAndRun(t *testing.T) {
	body := []byte(cleanDemoScript)
	s, home, ts := installScriptWorld(t, body, sha256Hex(body))
	ctx := context.Background()

	// Rejected forms: http://, command substitution, redirect to http://.
	if _, err := s.PlanCustomInstall(ctx, "box", "http://example.invalid/install.sh", ""); err == nil {
		t.Fatal("expected http:// to be rejected")
	} else {
		wantCode(t, err, "install_custom_insecure_url")
	}
	if _, err := s.PlanCustomInstall(ctx, "box", "bash -c \"$(curl -fsSL "+ts.URL+"/install.sh)\"", ""); err == nil {
		t.Fatal("expected command substitution to be rejected")
	}
	if _, err := s.PlanCustomInstall(ctx, "box", ts.URL+"/redirect-http", ""); err == nil {
		t.Fatal("expected https->http redirect downgrade to be rejected")
	} else {
		wantCode(t, err, "install_custom_insecure_url")
	}

	// Valid curl | bash with args: unpacked to client download + staged file execution.
	raw := "curl -fsSL " + ts.URL + "/setup.sh | bash -s -- --ver custom"
	plan, err := s.PlanCustomInstall(ctx, "box", raw, "")
	if err != nil {
		t.Fatalf("PlanCustomInstall: %v", err)
	}
	if plan.Refused || plan.NeedsConfirm || plan.SHA256 != sha256Hex(body) || plan.ScriptName != "setup.sh" {
		t.Fatalf("unexpected custom plan: %+v", plan)
	}
	cmdStr := strings.Join(plan.Command, " ")
	if strings.Contains(cmdStr, "curl") || !strings.Contains(cmdStr, "setup.sh --ver custom") {
		t.Fatalf("server command must execute staged script file, got %q", cmdStr)
	}

	var out bytes.Buffer
	res, err := s.RunCustomInstall(ctx, plan, "", InstallIO{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &out})
	if err != nil {
		t.Fatalf("RunCustomInstall: %v\n%s", err, out.String())
	}
	if !res.Completed || res.Found == nil || res.Found.ProfileID != "demo" {
		t.Fatalf("expected detected demo: %+v", res)
	}
	if !strings.Contains(out.String(), "installed args=--ver custom") {
		t.Fatalf("staged script did not receive args: %q", out.String())
	}
	staged := filepath.Join(home, ".local/state/valise/install", plan.ID, "setup.sh")
	if _, err := os.Stat(staged); err != nil {
		t.Fatalf("staged script missing: %v", err)
	}
}

func TestCustomInstallRiskSentenceAndRefusal(t *testing.T) {
	// Script with finding requires RiskSentence; refused script is always blocked.
	warnScript := []byte(cleanDemoScript + "echo download https://cdn.example.invalid/a.tgz\n")
	s, _, ts := installScriptWorld(t, warnScript, strings.Repeat("a", 64))
	ctx := context.Background()

	plan, err := s.PlanCustomInstall(ctx, "box", ts.URL+"/warn.sh", "")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Refused || !plan.NeedsConfirm {
		t.Fatalf("expected NeedsConfirm=true, Refused=false: %+v", plan)
	}
	var out bytes.Buffer
	if _, err := s.RunCustomInstall(ctx, plan, "wrong", InstallIO{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &out}); err == nil {
		t.Fatal("expected missing risk sentence to fail")
	} else {
		wantCode(t, err, "install_risk_sentence")
	}
	if res, err := s.RunCustomInstall(ctx, plan, install.RiskSentence, InstallIO{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &out}); err != nil || !res.Completed {
		t.Fatalf("RunCustomInstall with risk sentence: res=%+v err=%v", res, err)
	}

	// In-script secondary curl|sh is a high-risk warning (not hard-refused) with line & snippet.
	pipeScript := []byte(cleanDemoScript + "curl -fsSL https://uv.example.invalid/x | sh\n")
	sPipe, _, tsPipe := installScriptWorld(t, pipeScript, strings.Repeat("a", 64))
	pipePlan, err := sPipe.PlanCustomInstall(ctx, "box", "curl -fsSL "+tsPipe.URL+"/pipe.sh | bash", "")
	if err != nil {
		t.Fatal(err)
	}
	if pipePlan.Refused || !pipePlan.NeedsConfirm || len(pipePlan.Findings) == 0 || pipePlan.Findings[0].Line == 0 || pipePlan.Findings[0].Snippet == "" {
		t.Fatalf("expected high-risk warning with line/snippet for in-script pipe: %+v", pipePlan)
	}

	// Destructive script body downloaded from HTTPS URL is hard-refused.
	evil := []byte("#!/bin/sh\nrm -rf /\n")
	s2, _, ts2 := installScriptWorld(t, evil, strings.Repeat("a", 64))
	evilPlan, err := s2.PlanCustomInstall(ctx, "box", "curl -fsSL "+ts2.URL+"/evil.sh | bash", "")
	if err != nil {
		t.Fatal(err)
	}
	if !evilPlan.Refused {
		t.Fatalf("expected Refused=true for evil script: %+v", evilPlan)
	}
	if _, err := s2.RunCustomInstall(ctx, evilPlan, install.RiskSentence, InstallIO{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &out}); err == nil {
		t.Fatal("expected refused custom script to be blocked even with sentence")
	} else {
		wantCode(t, err, "install_custom_refused")
	}
}

func TestSavedCustomInstallsCRUD(t *testing.T) {
	s := newService(t, "ssh")

	// Empty initially.
	list, err := s.SavedInstalls()
	if err != nil || len(list) != 0 {
		t.Fatalf("initial SavedInstalls: %+v %v", list, err)
	}

	// Rejects insecure HTTP URL cleanly.
	if _, err := s.SaveCustomInstall("", "http://example.invalid/install.sh", ""); err == nil {
		t.Fatal("expected insecure http URL rejected")
	} else {
		wantCode(t, err, "install_custom_insecure_url")
	}

	// Save script URL without explicit name -> derives host/filename.
	saved1, err := s.SaveCustomInstall("", "curl -fsSL https://install.example.invalid/setup.sh | bash", "demo")
	if err != nil {
		t.Fatalf("SaveCustomInstall script: %v", err)
	}
	if saved1.ID == "" || saved1.Name != "install.example.invalid/setup.sh" || saved1.ProfileID != "demo" {
		t.Fatalf("unexpected saved1: %+v", saved1)
	}

	// Save argv command without explicit name -> derives first 2 tokens.
	saved2, err := s.SaveCustomInstall("", "npm install -g example-cli", "")
	if err != nil {
		t.Fatalf("SaveCustomInstall argv: %v", err)
	}
	if saved2.ID == "" || saved2.Name != "npm install" {
		t.Fatalf("unexpected saved2: %+v", saved2)
	}

	// Save explicit name updates existing raw+profile entry preserving ID.
	updated, err := s.SaveCustomInstall("My Installer", "curl -fsSL https://install.example.invalid/setup.sh | bash", "demo")
	if err != nil {
		t.Fatalf("SaveCustomInstall update: %v", err)
	}
	if updated.ID != saved1.ID || updated.Name != "My Installer" {
		t.Fatalf("expected updated entry with same ID %s, got %+v", saved1.ID, updated)
	}

	list, err = s.SavedInstalls()
	if err != nil || len(list) != 2 {
		t.Fatalf("expected 2 saved installs, got %+v %v", list, err)
	}

	// Delete one.
	if err := s.DeleteCustomInstall(saved1.ID); err != nil {
		t.Fatalf("DeleteCustomInstall: %v", err)
	}
	list, err = s.SavedInstalls()
	if err != nil || len(list) != 1 || list[0].ID != saved2.ID {
		t.Fatalf("expected only saved2 remaining, got %+v %v", list, err)
	}
}
