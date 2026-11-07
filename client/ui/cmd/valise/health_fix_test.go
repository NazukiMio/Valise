package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"valise/testbed/fakesshenv"
	"valise/ui/service"
)

// fake-ssh only proves Valise's side; real loginctl/timedatectl are
// target-machine checks.
func TestCLIHealthFix(t *testing.T) {
	e := fakesshenv.New(t)
	home := e.AddHost("box")
	e.InstallPorter("box")
	bin := filepath.Join(home, ".local/bin")
	os.WriteFile(filepath.Join(bin, "loginctl"), []byte(`#!/bin/sh
case "$1" in
 show-user) cat "$HOME/linger" 2>/dev/null || echo no ;;
 enable-linger) echo "$*" >> "$HOME/ran"; printf yes > "$HOME/linger" ;;
esac
`), 0o755)
	os.WriteFile(filepath.Join(bin, "timedatectl"), []byte("#!/bin/sh\nprintf 'CanNTP=yes\\nNTP=no\\nNTPSynchronized=no\\n'\n"), 0o755)
	os.WriteFile(filepath.Join(bin, "systemctl"), []byte("#!/bin/sh\necho loaded\n"), 0o755)
	cfg := t.TempDir()
	os.WriteFile(filepath.Join(cfg, "config.json"), []byte(`{"ssh_binary":"`+e.SSH+`"}`), 0o600)
	ran := func() string { b, _ := os.ReadFile(filepath.Join(home, "ran")); return string(b) }

	// No advanced mode on an ssh-config server: commands only.
	out, errOut, code := runCLI(t, cfg, "", "health", "fix", "box", "demo", "clock")
	if code != 0 || !strings.Contains(out, "sudo timedatectl set-ntp true") || !strings.Contains(out, "sudo systemctl restart systemd-timesyncd") ||
		!strings.Contains(out, "Valise 不能代为执行") || ran() != "" {
		t.Fatalf("code=%d\n%s\n%s", code, out, errOut)
	}
	// Declined: nothing runs.
	out, _, code = runCLI(t, cfg, "n\n", "health", "fix", "box", "demo", "linger")
	if code == 0 || !strings.Contains(out, "loginctl enable-linger") || !strings.Contains(out, "让 systemd 在你登出后继续运行你的用户服务") ||
		!strings.Contains(out, "撤销：loginctl disable-linger") || ran() != "" {
		t.Fatalf("code=%d\n%s", code, out)
	}
	out, _, code = runCLI(t, cfg, "", "health", "fix", "box", "demo", "linger", "--digest", "stale", "--yes")
	if code == 0 || ran() != "" {
		t.Fatalf("stale digest ran: %s", out)
	}
	out, errOut, code = runCLI(t, cfg, "y\n", "health", "fix", "box", "demo", "linger")
	if code != 0 || !strings.Contains(out, "退出码 0") || !strings.Contains(out, "该问题复核结果：本次检查通过") || !strings.Contains(ran(), "enable-linger") {
		t.Fatalf("code=%d\n%s\n%s", code, out, errOut)
	}
	if b, _ := os.ReadFile(filepath.Join(home, ".local/state/valise/audit.jsonl")); !strings.Contains(string(b), `"op":"system_fix"`) {
		t.Fatalf("audit %s", b)
	}
}

func TestPrintSystemFixResult(t *testing.T) {
	var out bytes.Buffer
	c := &cli{out: &out}
	c.printSystemFixResult(&service.SystemFixResult{
		Steps: []service.SystemStepResult{{Command: []string{"sudo", "timedatectl", "set-ntp", "true"}, Ran: true},
			{Command: []string{"sudo", "systemctl", "restart", "systemd-timesyncd"}, Note: "已经对上时间，不需要执行"}},
		Verification: &service.HealthVerification{State: "unknown", Text: "无法复核"}, Next: "稍后刷新"})
	for _, w := range []string{"✓ 命令：sudo timedatectl set-ntp true · 退出码 0", "未执行：sudo systemctl restart", "· 该问题复核结果：无法复核", "下一步：稍后刷新", "审计记录没有写入"} {
		if !strings.Contains(out.String(), w) {
			t.Fatalf("lacks %q:\n%s", w, out.String())
		}
	}
}
