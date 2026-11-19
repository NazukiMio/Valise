package gui

import (
	"context"
	"errors"
	"io"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

	"valise/core/valise"
	"valise/ui/service"
)

// The server's own command line (docs/INSTALL.md): a fresh management
// login opens the login shell in the window's terminal. It is not tied to
// an Agent; Valise passes bytes through and reads, keeps and records none.

type serverShellRun struct {
	server           string
	term             *terminal.Terminal
	termClose        io.Closer
	conn             *installConn
	running, stopped bool
	acceptRisk       bool
	cancel           context.CancelFunc
	code             int
	err              error
	exit             func()
}

func (r *serverShellRun) active() bool {
	return r != nil && r.running && !r.stopped
}

func (a *App) serverShellPage(c *ui.Context, server string) {
	ui.Row(c).Children(func() { backLink(c, server, "/servers/"+server) })
	pageHead(c, T("server.shell_title"), server)
	r := a.serverShell
	if r.active() && r.server != server {
		ui.Text(c, T("server.shell_other_running", "server", r.server)).Wrap()
		ui.Button(c, T("server.shell_return")).OnClick(func() { a.navigate("/servers/" + r.server + "/shell") })
		return
	}
	if r != nil && r.server == server {
		a.serverShellRunView(c, r)
		return
	}
	muted(ui.Text(c, T("server.shell_privacy"))).Wrap()
	ui.Button(c, T("server.shell_open_go")).OnClick(func() { a.startServerShell(server, false) })
}

func (a *App) startServerShell(server string, acceptRisk bool) {
	if a.serverShell.active() {
		return
	}
	if old := a.serverShell; old != nil && old.termClose != nil {
		_ = old.termClose.Close()
	}
	r := &serverShellRun{server: server, code: -1}
	a.debugAction("server_shell_start", server)
	a.serverShell = r
	conn := newInstallConn()
	r.conn = conn
	var termErr error
	switch {
	case a.newTerminal != nil:
		r.term, termErr = a.newTerminal(conn)
		if r.term != nil {
			r.termClose = r.term
		}
	case a.newNativeTestTerminal != nil:
		r.termClose, termErr = a.newNativeTestTerminal(conn)
	}
	if r.termClose == nil {
		conn.Close()
		if termErr != nil {
			r.err = valise.New("native_terminal_unavailable", T("native.terminal_unavailable"), T("native.terminal_next")).WithDetail(termErr.Error())
		} else {
			r.err = valise.New("native_terminal_unavailable", T("native.terminal_unavailable"), T("native.terminal_next"))
		}
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	if acceptRisk {
		ctx = service.WithRiskAccepted(ctx)
	}
	r.cancel, r.running = cancel, true
	a.run(func() {
		code, err := a.svc.RunServerShell(ctx, server, service.NativeIO{Stdin: conn.in, Stdout: conn.outW, Stderr: conn.outW})
		conn.Close()
		cancel()
		a.update(func() {
			r.running, r.code = false, code
			if !r.stopped && !errors.Is(err, context.Canceled) {
				r.err = err
			}
			if r.exit != nil {
				r.exit()
			}
		})
	})
}

// stopServerShell shows the ended state at once; the remote side catches
// up on its own.
func (a *App) stopServerShell(r *serverShellRun) {
	if r == nil {
		return
	}
	a.debugAction("server_shell_disconnect", r.server)
	r.stopped = true
	if r.cancel != nil {
		r.cancel()
	}
	if r.conn != nil {
		_ = r.conn.Close()
	}
}

func (a *App) closeServerShell(r *serverShellRun) {
	if r.active() || a.serverShell != r {
		return
	}
	if r.termClose != nil {
		_ = r.termClose.Close()
	}
	a.serverShell = nil
}

func (a *App) serverShellRunView(c *ui.Context, r *serverShellRun) {
	gated := false
	if e, ok := valise.As(r.err); ok && e.Code == service.GateCode {
		gated = true
	}
	card(ui.Column(c)).Gap(10).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			switch {
			case r.stopped:
				ui.Text(c, T("native.disconnected")).Grow(1)
			case r.running:
				ui.Text(c, T("server.shell_running")).Grow(1)
				ui.Button(c, T("native.disconnect")).OnClick(func() { a.stopServerShell(r) })
				return
			case r.code >= 0:
				ui.Text(c, T("server.shell_exited", "code", r.code)).Grow(1)
			default:
				ui.Text(c, T("native.not_started")).Grow(1)
			}
			if !gated {
				ui.PrimaryButton(c, T("native.reconnect")).OnClick(func() { a.startServerShell(r.server, false) })
			}
			ui.Button(c, T("native.to_prepare")).OnClick(func() { a.closeServerShell(r) })
		})
		if r.term != nil {
			terminal.View(c, r.term).Height(360).AutoFocus()
		}
		if r.err != nil {
			if gated {
				ui.Text(c, T("server.shell_backup_needed")).Wrap()
				ui.Button(c, T("native.go_backup")).OnClick(func() { a.navigate("/agents") })
				ui.Checkbox(c, &r.acceptRisk, T("native.accept_risk"))
				if r.acceptRisk {
					ui.Button(c, T("native.continue_once")).OnClick(func() { a.startServerShell(r.server, true) })
				}
			} else {
				errorBox(c, r.err, a.fold("server-shell-error"))
			}
		}
		if r.code >= 0 || r.stopped {
			muted(ui.Text(c, T("server.shell_result_hint"))).Wrap()
		}
	})
}

func (a *App) serverShellBanner(c *ui.Context) {
	r := a.serverShell
	if !r.active() {
		return
	}
	banner(c, "busy", bannerText(c, T("server.shell_banner", "server", r.server), ""), func() {
		ui.Button(c, T("server.shell_return")).OnClick(func() { a.navigate("/servers/" + r.server + "/shell") })
		quietButton(c, T("native.disconnect")).OnClick(func() { a.stopServerShell(r) })
	})
}
