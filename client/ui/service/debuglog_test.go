package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"valise/client/config"
	"valise/client/ssh"
	"valise/core/identity"
	"valise/core/protection"
	"valise/core/rpc"
	"valise/core/valise"
	"valise/testbed/fakesshenv"
)

func parseDebugEvents(t *testing.T, data string) []DebugEvent {
	t.Helper()
	var out []DebugEvent
	for _, line := range strings.Split(strings.TrimSpace(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var ev DebugEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("invalid debug JSONL line %q: %v", line, err)
		}
		if _, err := time.Parse(time.RFC3339Nano, ev.TS); err != nil {
			t.Fatalf("invalid RFC3339Nano timestamp %q: %v", ev.TS, err)
		}
		out = append(out, ev)
	}
	return out
}

func TestDebugLogDefaultsAndEnvOverride(t *testing.T) {
	t.Setenv(debugEnvVar, "")

	cases := []struct {
		version string
		want    bool
	}{
		{"0.0.0-dev", true},
		{"v0.1.0-DEV+abc", true},
		{"test", true},
		{"0.0.0-stage-Test", true},
		{"v1.0.0", false},
		{"0.9.2", false},
		{"", false},
	}
	for _, tc := range cases {
		s := &Service{Dirs: config.Dirs{Data: t.TempDir()}, Version: tc.version}
		if got := s.DebugLogEnabled(); got != tc.want {
			t.Errorf("Version=%q: DebugLogEnabled()=%v, want %v", tc.version, got, tc.want)
		}
	}

	// Release version is off by default and writes nothing.
	relDir := t.TempDir()
	rel, err := New(config.Dirs{Data: relDir, Cache: t.TempDir()}, "v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	rel.LogUIEvent("route", "/settings")
	if _, err := os.Stat(rel.DebugLogPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected no log file when disabled, got err=%v", err)
	}

	// SetDebugLog persists and overrides version default.
	if err := rel.SetDebugLog(true); err != nil {
		t.Fatal(err)
	}
	if !rel.DebugLogEnabled() {
		t.Fatal("expected enabled after SetDebugLog(true)")
	}
	reloaded, err := New(config.Dirs{Data: relDir, Cache: t.TempDir()}, "v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.DebugLogEnabled() {
		t.Fatal("expected enabled after reload from config.json")
	}

	if err := reloaded.SetDebugLog(false); err != nil {
		t.Fatal(err)
	}
	if reloaded.DebugLogEnabled() {
		t.Fatal("expected disabled after SetDebugLog(false)")
	}

	// VALISE_DEBUG=1 / 0 overrides both config and version.
	t.Setenv(debugEnvVar, "1")
	if !reloaded.DebugLogEnabled() {
		t.Fatal("VALISE_DEBUG=1 did not enable")
	}
	dev := &Service{Dirs: config.Dirs{Data: t.TempDir()}, Version: "0.0.0-dev"}
	t.Setenv(debugEnvVar, "0")
	if dev.DebugLogEnabled() {
		t.Fatal("VALISE_DEBUG=0 did not disable dev build")
	}
}

func TestDebugLogRotationAndRecent(t *testing.T) {
	t.Setenv(debugEnvVar, "")
	s := newService(t, "ssh")
	s.debug.maxBytes = 250

	// Missing log returns empty string cleanly.
	recent, err := s.RecentDebugLogs(50)
	if err != nil || recent != "" {
		t.Fatalf("expected empty recent logs, got %q %v", recent, err)
	}

	for i := 0; i < 12; i++ {
		s.LogUIEvent("click", fmt.Sprintf("item-%02d", i))
	}

	b1, err := os.ReadFile(s.DebugLogPath() + ".1")
	if err != nil {
		t.Fatalf("expected rotated .1 file: %v", err)
	}
	b0, err := os.ReadFile(s.DebugLogPath())
	if err != nil {
		t.Fatalf("expected active log file: %v", err)
	}
	ev1 := parseDebugEvents(t, string(b1))
	ev0 := parseDebugEvents(t, string(b0))
	if len(ev1) == 0 || len(ev0) == 0 {
		t.Fatalf("expected events in both files: .1=%d cur=%d", len(ev1), len(ev0))
	}

	// RecentDebugLogs spans .1 + current and honors maxLines.
	allRecent, err := s.RecentDebugLogs(50)
	if err != nil {
		t.Fatal(err)
	}
	allEv := parseDebugEvents(t, allRecent)
	if len(allEv) <= len(ev0) {
		t.Fatalf("expected RecentDebugLogs to include .1 and current: got %d, cur %d", len(allEv), len(ev0))
	}
	tail, err := s.RecentDebugLogs(3)
	if err != nil {
		t.Fatal(err)
	}
	tailEv := parseDebugEvents(t, tail)
	if len(tailEv) != 3 || tailEv[2].Detail != "item-11" {
		t.Fatalf("expected last 3 lines ending with item-11, got %+v", tailEv)
	}

	// OpenDebugLogDir creates the logs directory and invokes OS opener hook.
	var opened string
	old := openDirCmd
	openDirCmd = func(dir string) error { opened = dir; return nil }
	t.Cleanup(func() { openDirCmd = old })
	if err := s.OpenDebugLogDir(); err != nil {
		t.Fatal(err)
	}
	if opened != s.DebugLogDir() {
		t.Fatalf("opened %q, want %q", opened, s.DebugLogDir())
	}
	if st, err := os.Stat(s.DebugLogDir()); err != nil || !st.IsDir() {
		t.Fatalf("log dir missing: %v", err)
	}
}

func TestDebugLogRPCSshAskpassAndStages(t *testing.T) {
	t.Setenv(debugEnvVar, "")
	e := fakesshenv.New(t)
	e.AddHost("box")
	porter := e.InstallPorter("box")
	s := newService(t, e.SSH)
	if _, err := s.AddServers("box"); err != nil {
		t.Fatal(err)
	}
	if err := s.withSession(context.Background(), "box", func(c caller) error {
		var out any
		if err := c.Call(context.Background(), rpc.MethodHello, rpc.HelloParams{Protocol: rpc.ProtocolVersion}, &out); err != nil {
			return err
		}
		_ = c.Call(context.Background(), "unknown_method", nil, nil)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Askpass relay logs prompt category & redacted template.
	s.PasswordAuth.Prompt = func(string) (string, error) { return "pw-1", nil }
	env, err := s.relayEnv()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runHelper(t, env, "[sudo] password for alice:"); err != nil {
		t.Fatal(err)
	}

	// Backup controller logs prepare/confirm/run/succeeded stages via OnStage.
	b := newBackupBoundary()
	bc, err := NewBackupController(BackupControllerConfig{
		StateDir: filepath.Join(s.Dirs.Data, "bc"),
		RPC:      b.rpc,
		Review:   approveBackup,
		OnStage:  s.LogStage,
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := bc.PrepareBackup(context.Background(), "box", b.plan.AgentRef)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bc.StartBackup(context.Background(), plan.PlanID, "sub-1"); err != nil {
		t.Fatal(err)
	}

	// Also exercise direct SSH Run with ModeUser via userTransport.
	tr, _, err := s.userTransport("box")
	if err != nil {
		t.Fatal(err)
	}
	if tr.Mode() != ssh.ModeUser {
		t.Fatalf("expected ModeUser, got %q", tr.Mode())
	}
	if _, err := tr.Run(context.Background(), porter+" version --json", nil); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(s.DebugLogPath())
	if err != nil {
		t.Fatal(err)
	}
	events := parseDebugEvents(t, string(raw))

	var hasSSHStart, hasSSHEnd, hasRPCOk, hasRPCErr, hasAskpass, hasBackupStage bool
	for _, ev := range events {
		switch ev.Kind {
		case "ssh":
			if ev.Phase == "start" && ev.Host == "box" && ev.Mode == ssh.ModeUser {
				hasSSHStart = true
			}
			if ev.Phase == "end" && ev.Host == "box" && ev.Mode == ssh.ModeUser && ev.ExitCode != nil && *ev.ExitCode == 0 && ev.DurationMS != nil {
				hasSSHEnd = true
			}
		case "rpc":
			if ev.Method == rpc.MethodHello && ev.Host == "box" && ev.ErrCode == "" && ev.DurationMS != nil {
				hasRPCOk = true
			}
			if ev.Method == "unknown_method" && ev.Host == "box" && ev.ErrCode == "rpc_-32601" {
				hasRPCErr = true
			}
		case "askpass":
			if ev.PromptKind == "sudo_password" && ev.Template == "[sudo] password prompt" {
				hasAskpass = true
			}
		case "stage":
			if ev.Op == "backup" && ev.Status == "succeeded" && ev.Target == "box" {
				hasBackupStage = true
			}
		}
	}
	if !hasSSHStart || !hasSSHEnd || !hasRPCOk || !hasRPCErr || !hasAskpass || !hasBackupStage {
		t.Fatalf("missing expected log events: sshStart=%v sshEnd=%v rpcOk=%v rpcErr=%v askpass=%v backup=%v\nlogs:\n%s",
			hasSSHStart, hasSSHEnd, hasRPCOk, hasRPCErr, hasAskpass, hasBackupStage, string(raw))
	}
}

func TestDebugLogRestoreAndRestoreTestStages(t *testing.T) {
	t.Setenv(debugEnvVar, "")
	s, _, _, _ := sealedWorld(t)
	s.Version = "test"
	if _, err := s.AddServers("box"); err != nil {
		t.Fatal(err)
	}
	s.OwnerAuth.Passphrase = func(string) (string, error) { return pass, nil }
	if res, err := s.RunRestoreTest(context.Background(), "box", "demo", ""); err != nil || !res.OK {
		t.Fatalf("RunRestoreTest failed: %+v %v", res, err)
	}
	_, p, _ := desktopRestorePlan(t)
	// Point desktopRestorePlan's service at same test debug logging.
	pSvc, pPlan, _ := desktopRestorePlan(t)
	_ = p
	if _, err := pSvc.RunRestore(context.Background(), pPlan, proceedConfirm(), nil); err != nil {
		t.Fatal(err)
	}

	rawTest, _ := os.ReadFile(s.DebugLogPath())
	var sawTestLocal, sawTestServer, sawTestFinish bool
	for _, ev := range parseDebugEvents(t, string(rawTest)) {
		if ev.Kind == "stage" && ev.Op == "restore_test" {
			switch {
			case ev.Stage == TestLocal && ev.Status == "passed":
				sawTestLocal = true
			case ev.Stage == TestServer && ev.Status == "running":
				sawTestServer = true
			case ev.Stage == "finish" && ev.Status == "passed":
				sawTestFinish = true
			}
		}
	}
	if !sawTestLocal || !sawTestServer || !sawTestFinish {
		t.Fatalf("missing restore_test stages: local=%v server=%v finish=%v\n%s", sawTestLocal, sawTestServer, sawTestFinish, string(rawTest))
	}

	rawRestore, _ := os.ReadFile(pSvc.DebugLogPath())
	var sawInspect, sawConfirm, sawSwap, sawSucceeded bool
	for _, ev := range parseDebugEvents(t, string(rawRestore)) {
		if ev.Kind == "stage" && ev.Op == "restore" {
			switch {
			case ev.Stage == "inspect":
				sawInspect = true
			case ev.Stage == "confirm" && ev.Status == "waiting":
				sawConfirm = true
			case ev.Stage == "swap" && ev.Status == "running":
				sawSwap = true
			case ev.Status == "succeeded":
				sawSucceeded = true
			}
		}
	}
	if !sawInspect || !sawConfirm || !sawSwap || !sawSucceeded {
		t.Fatalf("missing restore stages: inspect=%v confirm=%v swap=%v succeeded=%v\n%s", sawInspect, sawConfirm, sawSwap, sawSucceeded, string(rawRestore))
	}
}

func TestDebugLogSensitiveDataRedaction(t *testing.T) {
	t.Setenv(debugEnvVar, "")
	s := newService(t, "ssh")
	s.debug.maxBytes = 320

	const (
		plainPass   = "PlainPassword!2026"
		keyPass     = "SshKeyPassphrase!99"
		keyPath     = "/Users/alice/.ssh/secret_ed25519"
		apiToken    = "sk-secret-token-123"
		privatePEM  = "-----BEGIN OPENSSH PRIVATE KEY-----"
		userContent = "user-file-secret-payload"
	)

	// 1. ClassifyAskpassPrompt templates for all 4 categories.
	askCases := []struct {
		prompt   string
		wantKind string
		wantTmpl string
	}{
		{"Enter passphrase for key '" + keyPath + "':", "key_passphrase", "Enter passphrase for key <redacted>"},
		{"[sudo] password for alice:", "sudo_password", "[sudo] password prompt"},
		{"alice@prod.example.com's password:", "server_password", "<user>@<host>'s password"},
		{"Verification code for " + apiToken + ":", "generic", "ssh askpass prompt"},
	}
	for _, tc := range askCases {
		kind, tmpl := ClassifyAskpassPrompt(tc.prompt)
		if kind != tc.wantKind || tmpl != tc.wantTmpl {
			t.Fatalf("ClassifyAskpassPrompt(%q) = (%q, %q), want (%q, %q)", tc.prompt, kind, tmpl, tc.wantKind, tc.wantTmpl)
		}
	}

	// 2. Askpass relay receiving encrypted key prompt and user passphrase + password.
	s.PasswordAuth.Prompt = func(p string) (string, error) {
		if strings.Contains(p, "passphrase") {
			return keyPass, nil
		}
		return plainPass, nil
	}
	env, err := s.relayEnv()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runHelper(t, env, "Enter passphrase for key '"+keyPath+"':"); err != nil {
		t.Fatal(err)
	}
	if _, err := runHelper(t, env, "alice@192.0.2.10's password: "); err != nil {
		t.Fatal(err)
	}

	// 3. RPC errors carrying secrets in Reason / Detail / Error() must only log ErrCode.
	rpcErr1 := valise.New("ssh_auth_failed",
		"failed for "+keyPath+" password="+plainPass+" token "+apiToken,
		userContent).WithDetail(privatePEM + "\n" + keyPass)
	rpcErr2 := fmt.Errorf("raw error: %s %s %s %s %s", plainPass, keyPass, keyPath, apiToken, privatePEM)
	s.logRPC("vault_status", "box", 12, rpcErr1)
	s.logRPC("protection_backup", "box", 25, rpcErr2)

	// 4. Stage & UI logs receiving accidental sensitive strings (multiple times to trigger rotation).
	for i := 0; i < 6; i++ {
		s.LogStage("backup", "copy", keyPath, "failed", privatePEM)
		s.LogStage("restore", "stage", "box", "failed", apiToken)
		s.LogUIEvent("action", "password="+plainPass+" passphrase="+keyPass+" "+keyPath+" "+apiToken+" "+privatePEM)
	}
	_ = identity.AgentRef{}
	_ = protection.Schema

	// Verify both valise-debug.log and valise-debug.log.1 exist and contain zero secrets.
	curBytes, err := os.ReadFile(s.DebugLogPath())
	if err != nil {
		t.Fatal(err)
	}
	rotBytes, err := os.ReadFile(s.DebugLogPath() + ".1")
	if err != nil {
		t.Fatalf("expected rotated .1 file during redaction test: %v", err)
	}
	parseDebugEvents(t, string(rotBytes))
	parseDebugEvents(t, string(curBytes))

	combined := string(rotBytes) + "\n" + string(curBytes)
	forbidden := []string{
		plainPass,
		keyPass,
		keyPath,
		"secret_ed25519",
		apiToken,
		privatePEM,
		userContent,
		"alice@192.0.2.10",
	}
	for _, secret := range forbidden {
		if strings.Contains(combined, secret) {
			t.Fatalf("debug log leaked forbidden sensitive text %q!\nlogs:\n%s", secret, combined)
		}
	}
}

func TestDebugLogStopsAfterServiceClose(t *testing.T) {
	t.Setenv(debugEnvVar, "")
	s := newService(t, "ssh")
	s.Close()
	s.LogUIEvent("after_close", "ignored")
	if _, err := os.Stat(s.DebugLogPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected no log file after Close(), got err=%v", err)
	}
}
