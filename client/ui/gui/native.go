package gui

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
	"valise/core/install"
	"valise/core/valise"
	"valise/ui/service"
)

type nativeState struct {
	read    job
	started bool
	info    *install.NativeInfo
	err     error
}

type nativeRun struct {
	server, instance string
	term             *terminal.Terminal
	conn             *installConn
	mode             service.NativeMode // the native CLI, or the instance's shell
	running, stopped bool
	acceptRisk       bool
	cancel           context.CancelFunc
	code             int
	err              error
	exit             func()
}

func (a *App) nativeOf(server, instance string) *nativeState {
	key := server + "/" + instance
	if a.natives == nil {
		a.natives = map[string]*nativeState{}
	}
	n := a.natives[key]
	if n == nil {
		n = &nativeState{}
		a.natives[key] = n
	}
	if !n.started {
		n.started = true
		var info *install.NativeInfo
		a.start(&n.read, time.Minute, func(ctx context.Context) (err error) {
			info, err = a.svc.InstanceNativeInfo(ctx, server, instance)
			return err
		}, func(err error) { n.info, n.err = info, err })
	}
	return n
}

func (a *App) nativeCard(c *ui.Context, row *agentRow) {
	if !row.HasNative || row.Retired() {
		return
	}
	n := a.nativeOf(row.Server, row.Instance)
	if n.read.busy {
		return
	}
	if e, ok := valise.As(n.err); ok && e.Code == "unsupported_capability" {
		return
	}
	if n.err == nil && (n.info == nil || len(n.info.Argv) == 0) {
		return
	}
	card(ui.Column(c)).Gap(8).Children(func() {
		ui.Text(c, T("native.title")).Bold()
		if n.err != nil {
			errorBox(c, n.err, a.fold("native-info-"+row.Server+row.Instance))
			ui.Button(c, T("common.refresh")).OnClick(func() { n.started = false })
			return
		}
		muted(ui.Text(c, T("native.privacy"))).Wrap()
		ui.Button(c, T("native.open")).OnClick(func() { a.navigate("/native/" + row.Server + "/" + row.Instance) })
	})
}

func (a *App) nativePage(c *ui.Context, server, instance string) {
	ui.Row(c).Children(func() { ui.Button(c, T("native.back")).OnClick(func() { a.openAgent(server, instance) }) })
	pageHead(c, T("native.title"), server+" · "+instance)
	n := a.nativeOf(server, instance)
	if n.read.busy {
		a.jobLine(c, &n.read)
		return
	}
	if n.err != nil {
		errorBox(c, n.err, a.fold("native-info"))
		return
	}
	r := a.native
	if r != nil && r.running && (r.server != server || r.instance != instance) {
		ui.Text(c, T("native.other_running")).Wrap()
		ui.Button(c, T("native.return")).OnClick(func() { a.navigate("/native/" + r.server + "/" + r.instance) })
		return
	}
	if r != nil && r.server == server && r.instance == instance {
		// A session, live or ended, is the page; the way back to the
		// entries is in its top bar.
		a.nativeRunView(c, r, n.info)
		return
	}
	if n.info == nil || len(n.info.Argv) == 0 {
		return
	}
	card(ui.Column(c)).Gap(8).Children(func() {
		muted(ui.Text(c, T("native.privacy"))).Wrap()
		for _, hint := range n.info.Hints {
			ui.Text(c, hint).Wrap()
		}
		ui.Collapsible(c, T("native.command"), a.fold("native-command"), func() { mono(ui.Text(c, strings.Join(n.info.Argv, " "))).Wrap() })
		ui.Row(c).Gap(8).Children(func() {
			ui.PrimaryButton(c, T("native.connect")).OnClick(func() { a.startNative(server, instance, service.NativeCLI) })
			if n.info.Shell { // only a porter that can open it
				ui.Button(c, T("native.shell")).OnClick(func() { a.startNative(server, instance, service.NativeShell) })
			}
		})
		if n.info.Shell {
			muted(ui.Text(c, T("native.shell_hint"))).Wrap()
		}
	})
}

func (a *App) startNative(server, instance string, mode service.NativeMode) {
	a.startNativeAccepted(server, instance, mode, false)
}

func (a *App) startNativeAccepted(server, instance string, mode service.NativeMode, acceptRisk bool) {
	if a.native != nil && a.native.running {
		return
	}
	if a.native != nil && a.native.term != nil {
		_ = a.native.term.Close()
	}
	r := &nativeRun{server: server, instance: instance, mode: mode, code: -1}
	a.native = r
	conn := newInstallConn()
	r.conn = conn
	if a.newTerminal != nil {
		r.term, r.err = a.newTerminal(conn)
	}
	if r.term == nil {
		conn.Close()
		r.err = valise.New("native_terminal_unavailable", T("native.terminal_unavailable"), T("native.terminal_next"))
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	if acceptRisk {
		ctx = service.WithRiskAccepted(ctx)
	}
	r.cancel, r.running = cancel, true
	a.run(func() {
		code, err := a.svc.RunInstanceNative(ctx, server, instance, mode, service.NativeIO{Stdin: conn.in, Stdout: conn.outW, Stderr: conn.outW})
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

func (a *App) stopNative(r *nativeRun) {
	r.stopped = true
	if r.cancel != nil {
		r.cancel()
	}
	if r.conn != nil {
		r.conn.Close()
	}
}

// nativeRunView is one session. While it runs the top bar ends it; once
// the remote program exited or the connection dropped, the bar itself
// offers what comes next, so an ended terminal never blocks the page.
func (a *App) nativeRunView(c *ui.Context, r *nativeRun, info *install.NativeInfo) {
	gated := false
	if e, ok := valise.As(r.err); ok && e.Code == service.GateCode {
		gated = true
	}
	card(ui.Column(c)).Gap(10).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			switch {
			case r.running:
				ui.Text(c, T("native.running")).Grow(1)
				ui.Button(c, T("native.disconnect")).OnClick(func() { a.stopNative(r) })
				return
			case r.stopped:
				ui.Text(c, T("native.disconnected")).Grow(1)
			case r.code >= 0:
				ui.Text(c, T("native.exited", "code", r.code)).Grow(1)
			default:
				ui.Text(c, T("native.not_started")).Grow(1)
			}
			if !gated { // the backup choice below is that case's way on
				ui.PrimaryButton(c, T("native.reconnect")).OnClick(func() { a.startNative(r.server, r.instance, r.mode) })
				// The other entry, so both are at hand here as on the
				// prepare page.
				switch {
				case r.mode == service.NativeShell:
					ui.Button(c, T("native.use_cli")).OnClick(func() { a.startNative(r.server, r.instance, service.NativeCLI) })
				case info != nil && info.Shell:
					ui.Button(c, T("native.use_shell")).OnClick(func() { a.startNative(r.server, r.instance, service.NativeShell) })
				}
			}
			ui.Button(c, T("native.to_prepare")).OnClick(func() { a.closeNative(r) })
		})
		if r.term != nil {
			terminal.View(c, r.term).Height(360).AutoFocus()
		}
		if r.err != nil {
			if gated {
				ui.Text(c, T("native.backup_needed")).Wrap()
				ui.Button(c, T("native.go_backup")).OnClick(func() { a.openAgent(r.server, r.instance); a.agentTab = tabManage })
				ui.Checkbox(c, &r.acceptRisk, T("native.accept_risk"))
				if r.acceptRisk {
					ui.Button(c, T("native.continue_once")).OnClick(func() { a.startNativeAccepted(r.server, r.instance, r.mode, true) })
				}
			} else {
				errorBox(c, r.err, a.fold("native-run-error"))
			}
		}
		if r.code >= 0 || r.stopped {
			muted(ui.Text(c, T("native.result_hint"))).Wrap()
		}
	})
}

// closeNative lets go of an ended session and shows the entries again.
func (a *App) closeNative(r *nativeRun) {
	if r.running || a.native != r {
		return
	}
	if r.term != nil {
		_ = r.term.Close()
	}
	a.native = nil
}

func (a *App) nativeBanner(c *ui.Context) {
	r := a.native
	if r == nil || !r.running {
		return
	}
	ui.Row(c).Padding(8).Gap(12).Children(func() {
		ui.Text(c, T("native.banner", "server", r.server, "instance", r.instance)).Grow(1)
		ui.Button(c, T("native.return")).OnClick(func() { a.navigate("/native/" + r.server + "/" + r.instance) })
		ui.Button(c, T("native.disconnect")).OnClick(func() { a.stopNative(r) })
	})
}
