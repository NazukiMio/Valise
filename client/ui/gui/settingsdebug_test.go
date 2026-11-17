package gui

import (
	"os"
	"strings"
	"testing"

	"valise/client/config"
)

func TestSettingsDebugToggleAndButtons(t *testing.T) {
	t.Setenv("VALISE_DEBUG", "")
	a, tt := world(t)
	a.svc.Version = "v1.0.0"

	opened := 0
	a.openDebugDir = func() error { opened++; return nil }

	var copied string
	a.SetClipboard(func(text string) error { copied = text; return nil })

	a.navigate("/settings")
	tt.Frame()
	hasAll(t, tt.Texts(), "诊断日志", "诊断日志", "已关闭", a.svc.DebugLogPath(), "打开日志目录", "复制最近诊断日志")

	// Copying when no log file exists shows friendly empty feedback.
	click(t, tt, "复制最近诊断日志")
	if copied != "" || !tt.HasText("暂无诊断日志可复制") {
		t.Fatalf("expected empty feedback without copy, copied=%q texts=%q", copied, tt.Texts())
	}

	// Enable debug logging via the Switch control.
	click(t, tt, "记录诊断日志")
	if !a.svc.DebugLogEnabled() || !tt.HasText("已开启") {
		t.Fatalf("expected debug log enabled, enabled=%v texts=%q", a.svc.DebugLogEnabled(), tt.Texts())
	}
	cfg, err := config.Load(a.svc.Dirs.Data)
	if err != nil || cfg.DebugLog == nil || !*cfg.DebugLog {
		t.Fatalf("expected persisted debug_log=true, got %+v %v", cfg, err)
	}

	// Route switches (both via navigate and direct router.Push) are recorded.
	a.navigate("/servers")
	tt.Frame()
	a.router.Push("/settings")
	tt.Frame()

	// Open log directory hook is invoked.
	click(t, tt, "打开日志目录")
	if opened != 1 {
		t.Fatalf("expected openDebugDir hook called once, got %d", opened)
	}

	// Copy recent debug logs places JSONL lines into clipboard and shows count.
	click(t, tt, "复制最近诊断日志")
	if copied == "" || !strings.Contains(copied, `"kind":"ui"`) || !strings.Contains(copied, "/settings -> /servers") {
		t.Fatalf("expected copied logs with route & ui events, got %q", copied)
	}
	if !tt.HasText("已复制最近") {
		t.Fatalf("expected copied feedback in texts: %q", tt.Texts())
	}

	// Disable debug logging via the Switch control.
	click(t, tt, "记录诊断日志")
	if a.svc.DebugLogEnabled() || !tt.HasText("已关闭") {
		t.Fatalf("expected debug log disabled, enabled=%v texts=%q", a.svc.DebugLogEnabled(), tt.Texts())
	}
	cfg, err = config.Load(a.svc.Dirs.Data)
	if err != nil || cfg.DebugLog == nil || *cfg.DebugLog {
		t.Fatalf("expected persisted debug_log=false, got %+v %v", cfg, err)
	}
}

func TestDebugRouteSanitization(t *testing.T) {
	t.Setenv("VALISE_DEBUG", "")
	a, tt := world(t)
	a.svc.Version = "0.0.0-dev"

	a.router.Push("/agents/alice:secretpw@box/xiaoman?token=sk-secret-token-123#frag")
	tt.Frame()
	a.navigate("/servers/sk-secret-token-123/agents?code=mysecret")
	tt.Frame()
	a.debugAction("native_connect", "password=my-pw-123")

	raw, err := os.ReadFile(a.svc.DebugLogPath())
	if err != nil {
		t.Fatal(err)
	}
	logs := string(raw)
	for _, secret := range []string{"secretpw", "sk-secret-token-123", "mysecret", "my-pw-123", "#frag"} {
		if strings.Contains(logs, secret) {
			t.Fatalf("GUI debug log leaked %q:\n%s", secret, logs)
		}
	}
	if !strings.Contains(logs, `"action":"route"`) || !strings.Contains(logs, `"action":"native_connect"`) {
		t.Fatalf("expected route and button actions in logs:\n%s", logs)
	}
}
