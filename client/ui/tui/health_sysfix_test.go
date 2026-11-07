package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"valise/ui/service"
)

// fake-ssh only: real loginctl/polkit behaviour needs a target machine.
const tuiLoginctl = `#!/bin/sh
case "$1" in
 show-user) cat "$HOME/linger" 2>/dev/null || echo no ;;
 enable-linger) echo "$*" >> "$HOME/ran"; printf yes > "$HOME/linger" ;;
esac
`

func TestSystemFixFlow(t *testing.T) {
	m, home := world(t)
	bin := filepath.Join(home, ".local/bin")
	os.WriteFile(filepath.Join(bin, "loginctl"), []byte(tuiLoginctl), 0o755)
	os.WriteFile(filepath.Join(bin, "timedatectl"), []byte("#!/bin/sh\nprintf 'CanNTP=yes\\nNTP=yes\\nNTPSynchronized=yes\\n'\n"), 0o755)
	unit := filepath.Join(home, ".config/systemd/user/valise-seal@demo.timer")
	os.MkdirAll(filepath.Dir(unit), 0o755)
	os.WriteFile(unit, []byte("[Timer]\nOnUnitActiveSec=21600s\n"), 0o644)
	if _, err := m.svc.Enroll(m.ctx, "box", service.EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatal(err)
	}
	openInstance(t, m)
	key(t, m, "4")
	p := m.top().(*instancePage)
	tab := p.tabs[p.active].(*healthTab)
	for i, it := range tab.rows(m, p) {
		if it.ID == service.FixLinger {
			tab.cursor = i
		}
	}
	wantText(t, m, "退出登录或服务器重启后，定时备份不会运行", "修复 · 开启 linger")
	key(t, m, "f")
	c, ok := m.top().(*systemFixConfirm)
	if !ok {
		t.Fatalf("not confirmation: %T", m.top())
	}
	user := c.fix.Steps[0].Command[2]
	wantText(t, m, "loginctl enable-linger "+user, "让 systemd 在你登出后继续运行你的用户服务", "撤销：loginctl disable-linger "+user, "新开一次管理登录")
	key(t, m, "esc")
	if _, err := os.Stat(filepath.Join(home, "ran")); !os.IsNotExist(err) {
		t.Fatal("cancel ran the fix")
	}
	key(t, m, "f")
	oldHealth := p.healthRead.seq
	key(t, m, "enter")
	r, ok := m.top().(*systemFixResult)
	if !ok || r.res == nil || r.res.Verification.State != "resolved" {
		t.Fatalf("result %T %+v", m.top(), r)
	}
	wantText(t, m, "命令：loginctl enable-linger "+user+" · 退出码 0", "该问题复核结果", "linger 已开启")
	if p.healthRead.seq <= oldHealth {
		t.Fatal("parent was not refreshed")
	}
	if b, _ := os.ReadFile(filepath.Join(home, ".local/state/valise/audit.jsonl")); !strings.Contains(string(b), "system_fix") {
		t.Fatal("no audit")
	}
}

func TestSystemFixConfirmAdvancedAndResult(t *testing.T) {
	m, _ := world(t)
	f := &service.SystemFix{ID: service.FixClock, Name: "开启时间同步", Goal: "对时", Impact: "不中断", Mode: service.FixAdvanced,
		Access: "高级模式", Steps: []service.SystemFixStep{
			{Command: []string{"sudo", "timedatectl", "set-ntp", "true"}, Change: "开启系统的自动时间同步（NTP）", Undo: "sudo timedatectl set-ntp false"},
			{Command: []string{"sudo", "systemctl", "restart", "systemd-timesyncd"}, Change: "重启", Undo: "不需要", When: "仅当开启后仍没有对上时间时执行"}}}
	v := (&systemFixConfirm{host: "box", inst: "demo", fix: f}).view(m)
	for _, w := range []string{"sudo timedatectl set-ntp true", "仅当开启后仍没有对上时间时执行", "sudo systemctl restart systemd-timesyncd", "开启高级模式", "改变："} {
		if !strings.Contains(v, w) {
			t.Fatalf("lacks %q:\n%s", w, v)
		}
	}
	res := &service.SystemFixResult{Steps: []service.SystemStepResult{{Command: f.Steps[0].Command, Ran: true}, {Command: f.Steps[1].Command, Ran: true, Exit: 5}},
		Verification: &service.HealthVerification{State: "remaining", Text: "问题仍在：时间同步还没有对上时间"}, Next: "几分钟后刷新", Audited: true}
	out := strings.Join(systemFixLines(res), "\n")
	if !strings.Contains(out, "退出码 0") || !strings.Contains(out, "退出码 5") || !strings.Contains(out, "问题仍在") || !strings.Contains(out, "下一步") {
		t.Fatal(out)
	}
	res.Verification = &service.HealthVerification{State: "unknown", Text: "无法复核"}
	res.Audited = false
	out = strings.Join(systemFixLines(res), "\n")
	if !strings.Contains(out, "无法复核") || !strings.Contains(out, "审计记录没有写入") {
		t.Fatal(out)
	}
}
