package gui

import (
	"context"
	"testing"
	"time"

	"valise/core/valise"
	"valise/ui/service"
)

func TestServerShellEntryPrepareAndMissingTerminal(t *testing.T) {
	a, tt := settingsWorld(t)
	a.router.Push("/servers/home")
	tt.Frame()
	click(t, tt, T("server.shell_open"))
	if a.router.Path() != "/servers/home/shell" {
		t.Fatalf("went to %s", a.router.Path())
	}
	hasAll(t, tt.Texts(), T("server.shell_title"), T("server.shell_privacy"), T("server.shell_open_go"))
	if a.serverShell != nil {
		t.Fatal("navigation started the shell")
	}
	click(t, tt, T("server.shell_open_go"))
	if a.serverShell == nil || a.serverShell.running || a.serverShell.err == nil || !tt.HasText(T("native.terminal_unavailable")) {
		t.Fatal("missing terminal started a shell", a.serverShell, tt.Texts())
	}
}

func TestServerShellGateNeedsExplicitRisk(t *testing.T) {
	a, tt := settingsWorld(t)
	r := &serverShellRun{server: "home", code: -1, err: valise.New(service.GateCode, "fixture gate", "fixture next")}
	a.serverShell = r
	a.router.Push("/servers/home/shell")
	tt.Frame()
	tt.Advance(time.Second)
	hasAll(t, tt.Texts(), T("server.shell_backup_needed"), T("native.go_backup"), T("native.accept_risk"))
	if tt.HasText(T("native.continue_once")) || a.svc.RiskAccepted {
		t.Fatal("risk accepted without a click")
	}
	click(t, tt, T("native.accept_risk"))
	if !tt.HasText(T("native.continue_once")) || a.serverShell != r || a.svc.RiskAccepted {
		t.Fatal("checkbox ran the shell or accepted the risk for good")
	}
	click(t, tt, T("native.go_backup"))
	if a.router.Path() != "/agents" {
		t.Fatalf("went to %s", a.router.Path())
	}
}

func TestServerShellDisconnectBannerAndExit(t *testing.T) {
	a, tt := settingsWorld(t)
	_, cancel := context.WithCancel(context.Background())
	r := &serverShellRun{server: "home", running: true, cancel: cancel, conn: newInstallConn(), code: -1}
	a.serverShell = r
	a.router.Push("/servers/home")
	tt.Frame()
	hasAll(t, tt.Texts(), T("server.shell_banner", "server", "home"), T("server.shell_return"))
	click(t, tt, T("server.shell_return"))
	if a.router.Path() != "/servers/home/shell" {
		t.Fatalf("went to %s", a.router.Path())
	}
	hasAll(t, tt.Texts(), T("server.shell_running"))
	click(t, tt, T("native.disconnect"))
	if !r.stopped || !tt.HasText(T("native.disconnected")) || tt.HasText(T("server.shell_running")) {
		t.Fatalf("disconnect did not switch the UI: %q", tt.Texts())
	}
	r.running = false
	// A shell that exited says its exit code, and not that anything is healthy.
	a.serverShell = &serverShellRun{server: "home", code: 130}
	tt.Frame()
	tt.Advance(time.Second)
	hasAll(t, tt.Texts(), T("server.shell_exited", "code", 130), T("native.reconnect"), T("native.to_prepare"), T("server.shell_result_hint"))
	click(t, tt, T("native.to_prepare"))
	if a.serverShell != nil || !tt.HasText(T("server.shell_open_go")) {
		t.Fatal("back to prepare kept the ended shell")
	}
}

func TestServerShellCloseCancelsAndClearsPrompts(t *testing.T) {
	a, _ := settingsWorld(t)
	ctx, cancel := context.WithCancel(context.Background())
	r := &serverShellRun{server: "home", running: true, cancel: cancel, conn: newInstallConn()}
	a.serverShell = r
	reply := make(chan passReply, 1)
	a.asks = []*passPrompt{{host: "home", kind: askAdmin, value: "fixture-passphrase", open: true, reply: reply}}
	closed := false
	if a.RequestClose(func() { closed = true }) || closed || ctx.Err() == nil || !r.stopped {
		t.Fatal("close did not cancel and wait")
	}
	if len(a.asks) != 0 {
		t.Fatal("close left a login prompt")
	}
	if got := <-reply; got.err == nil || got.pass != "" {
		t.Fatal("close answered a secret")
	}
	r.running = false
	r.exit()
	if !closed {
		t.Fatal("shell end did not release the window close")
	}
}
