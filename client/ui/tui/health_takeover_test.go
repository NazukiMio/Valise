package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"valise/ui/service"
)

// fake-ssh and stand-in screens only: real sudo is not exercised here.

func clockFix(nonInteractive bool) *service.SystemFix {
	pre := []string{"sudo"}
	if nonInteractive {
		pre = []string{"sudo", "-n"}
	}
	return &service.SystemFix{ID: service.FixClock, Name: "开启时间同步", Host: "box", Instance: "demo", Goal: "对时", Impact: "不中断",
		Mode: service.FixAdvanced, NonInteractive: nonInteractive, Access: "执行方式", Digest: "d",
		Manual: [][]string{{"sudo", "timedatectl", "set-ntp", "true"}},
		Steps: []service.SystemFixStep{
			{Command: append(append([]string{}, pre...), "timedatectl", "set-ntp", "true"), Change: "开启 NTP", Undo: "sudo timedatectl set-ntp false"},
			{Command: append(append([]string{}, pre...), "systemctl", "restart", "systemd-timesyncd"), Change: "重启", Undo: "不需要"}}}
}

// Takeover off: the sudo fix still goes to the terminal, unlabelled.
func TestTakeoverOffGoesToTerminal(t *testing.T) {
	m, _ := world(t)
	drive(t, m, m.push(&systemFixConfirm{host: "box", inst: "demo", fix: clockFix(false)}))
	v := wantText(t, m, "sudo timedatectl set-ntp true", "在终端里由你开启高级模式")
	if strings.Contains(v, service.SudoLabel) || strings.Contains(v, "接管模式") {
		t.Fatal(v)
	}
	key(t, m, "enter")
	if !strings.Contains(m.flash, "valise health fix box demo clock --advanced") {
		t.Fatalf("flash %q", m.flash)
	}
}

// Takeover on: every sudo command is labelled; a server where advanced
// mode cannot be turned on says why and runs nothing.
func TestTakeoverOnConfirmation(t *testing.T) {
	m, _ := world(t)
	drive(t, m, m.push(&systemFixConfirm{host: "box", inst: "demo", fix: clockFix(true)}))
	v := wantText(t, m, "sudo -n timedatectl set-ntp true", "sudo -n systemctl restart systemd-timesyncd", "接管模式", "不会在这里询问")
	if strings.Count(v, service.SudoLabel) != 2 || strings.Contains(v, "在终端里由你开启高级模式") {
		t.Fatal(v)
	}
	key(t, m, "enter")
	if !strings.Contains(m.flash, "接管模式要先为 box 开启高级模式") || !m.flashErr {
		t.Fatalf("flash %q", m.flash)
	}
	if _, ok := m.top().(*systemFixResult); ok {
		t.Fatal("ran without advanced mode")
	}
}

// A destructive step is marked on the confirmation; built-in ones are not.
func TestDestructiveStepMarked(t *testing.T) {
	m, _ := world(t)
	f := clockFix(false)
	v := (&systemFixConfirm{host: "box", inst: "demo", fix: f}).view(m)
	if strings.Contains(v, "破坏性") {
		t.Fatal(v)
	}
	f.Steps = append(f.Steps, service.SystemFixStep{Command: []string{"sudo", "rm", "-r", "/var/lib/example"}, Change: "删除", Undo: "不能撤销", Destructive: true})
	v = ansiRe.ReplaceAllString((&systemFixConfirm{host: "box", inst: "demo", fix: f}).view(m), "")
	if strings.Count(v, "破坏性") != 1 || !strings.Contains(v, "执行前会检查别处") {
		t.Fatal(v)
	}
}

// The in-TUI enable uses the confirm and masked input screens and hands
// the answers to the service's EnableAdvanced, whose checks decide.
func TestTakeoverEnableAdvancedUsesService(t *testing.T) {
	m, _ := world(t)
	called := false
	drive(t, m, enableAdvancedThen(m, "box", false, func(m *Model) tea.Cmd { called = true; return nil }))
	wantText(t, m, "为 box 开启高级模式？", service.AdvancedRisks[0], "只用于这次修复")
	key(t, m, "y")
	wantText(t, m, "Valise 口令")
	typeText(t, m, "valise secret pass")
	if strings.Contains(screenText(m), "valise secret pass") {
		t.Fatal("passphrase echoed")
	}
	key(t, m, "enter")
	wantText(t, m, "box 的管理口令")
	typeText(t, m, "admin secret pass")
	key(t, m, "enter")
	wantText(t, m, service.AdvancedConsent)
	typeText(t, m, service.AdvancedConsent)
	key(t, m, "enter")
	if called || m.svc.AdvancedHost() != "" || !strings.Contains(m.flash, "不是由 Valise 接入的服务器") {
		t.Fatalf("called=%v host=%q flash=%q", called, m.svc.AdvancedHost(), m.flash)
	}
}

// sudo -n asked for a password: the result says so and offers the terminal.
func TestTakeoverSudoPasswordResult(t *testing.T) {
	m, _ := world(t)
	f := clockFix(true)
	r := &systemFixResult{host: "box", inst: "demo", fix: f, res: &service.SystemFixResult{Fix: f.ID, SudoPassword: true,
		Terminal: "valise health fix box demo clock --advanced", Next: "这台服务器的 sudo 需要密码；…", Audited: true,
		Steps:        []service.SystemStepResult{{Command: f.Steps[0].Command, Note: "这台服务器的 sudo 需要密码，没有在这里执行"}},
		Verification: &service.HealthVerification{State: "remaining", Text: "问题仍在"}}}
	m.stack = append(m.stack, r)
	wantText(t, m, "这台服务器的 sudo 需要密码", "valise health fix box demo clock --advanced", "转到终端执行", "未执行")
	key(t, m, "t")
	if !strings.Contains(m.flash, "valise health fix box demo clock --advanced") {
		t.Fatalf("flash %q", m.flash)
	}
}

func TestTakeoverSetting(t *testing.T) {
	m, _ := world(t)
	if m.svc.Takeover("box") || !strings.Contains(takeoverRow(m, "box"), "关") {
		t.Fatal("default on")
	}
	drive(t, m, toggleTakeover(m, "box"))
	if !m.svc.Takeover("box") || !strings.Contains(m.flash, "接管模式已开启") || !strings.Contains(takeoverRow(m, "box"), "按 t 关掉") {
		t.Fatalf("%q", m.flash)
	}
	drive(t, m, toggleTakeover(m, "box"))
	if m.svc.Takeover("box") {
		t.Fatal("still on")
	}
}
