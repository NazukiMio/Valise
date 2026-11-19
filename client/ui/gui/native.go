package gui

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
	"valise/core/install"
	"valise/core/valise"
	"valise/ui/service"
)

type nativeState struct {
	read        job
	started     bool
	info        *install.NativeInfo
	err         error
	pausedUnits []string
	restoreBusy bool
	restoreRes  *service.NativeServiceRestoreResult
	restoreErr  error
}

type nativeRun struct {
	server, instance string
	term             *terminal.Terminal
	termClose        io.Closer
	conn             *installConn
	mode             service.NativeMode // onboarding CLI, daily session, or instance shell
	stopService      bool               // pause managed service before onboarding
	stoppingService  bool
	stoppedUnits     []string
	running, stopped bool
	acceptRisk       bool
	cancel           context.CancelFunc
	code             int
	err              error
	exit             func()
}

func (r *nativeRun) active() bool {
	return r != nil && r.running && !r.stopped
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
	if n.err == nil && !hasNativeEntry(n.info) {
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

func hasNativeEntry(info *install.NativeInfo) bool {
	return info != nil && (len(info.Argv) > 0 || len(info.SessionArgv) > 0)
}

func (a *App) nativePage(c *ui.Context, server, instance string) {
	ui.Row(c).Children(func() { backLink(c, T("native.back"), "/agents/"+server+"/"+instance) })
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
	if r.active() && (r.server != server || r.instance != instance) {
		ui.Text(c, T("native.other_running")).Wrap()
		ui.Button(c, T("native.return")).OnClick(func() { a.navigate("/native/" + r.server + "/" + r.instance) })
		return
	}
	if r != nil && r.server == server && r.instance == instance {
		a.nativeRunView(c, r, n)
		return
	}
	if !hasNativeEntry(n.info) {
		return
	}
	muted(ui.Text(c, T("native.privacy"))).Wrap()
	a.nativePausedBanner(c, server, instance, n)
	if len(n.info.SessionArgv) > 0 {
		a.nativeDailyCard(c, server, instance, n.info)
	} else if a.hasSessionCLI[n.info.ProfileID] {
		a.nativeSessionUpdateCard(c, server)
	}
	if len(n.info.Argv) > 0 {
		a.nativeOnboardCard(c, server, instance, n)
	}
}

func (a *App) nativeSessionUpdateCard(c *ui.Context, server string) {
	card(ui.Column(c)).Gap(8).Children(func() {
		sectionTitle(ui.Text(c, T("native.session")))
		muted(ui.Text(c, T("native.session_update_needed"))).Wrap()
		ui.Row(c).Children(func() {
			ui.Button(c, T("native.go_update_profile")).OnClick(func() { a.router.Push("/servers/" + server + "/agents") })
		})
	})
}

func (a *App) startNative(server, instance string, mode service.NativeMode, stopService bool) {
	a.startNativeAccepted(server, instance, mode, false, stopService)
}

func (a *App) startNativeAccepted(server, instance string, mode service.NativeMode, acceptRisk, stopService bool) {
	if a.native.active() {
		return
	}
	if a.native != nil && a.native.termClose != nil {
		_ = a.native.termClose.Close()
	}
	n := a.nativeOf(server, instance)
	r := &nativeRun{
		server: server, instance: instance, mode: mode,
		stopService: stopService, stoppingService: stopService,
		stoppedUnits: append([]string(nil), n.pausedUnits...),
		code:         -1,
	}
	a.debugAction("native_start", server+"/"+instance+"/"+string(mode))
	a.native = r
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
		if stopService {
			if !a.pauseServiceForNative(ctx, r) {
				conn.Close()
				cancel()
				return
			}
		}
		code, err := a.svc.RunInstanceNative(ctx, server, instance, mode, service.NativeIO{
			Stdin:    conn.in,
			Stdout:   conn.outW,
			Stderr:   conn.outW,
			PTY:      r.term != nil,
			OnResize: conn.SetResize,
		})
		conn.Close()
		cancel()
		var refresh bool
		a.update(func() {
			r.stoppingService, r.running, r.code = false, false, code
			if !r.stopped && !errors.Is(err, context.Canceled) {
				r.err = err
			}
			if r.exit != nil {
				r.exit()
			}
			refresh = len(r.stoppedUnits) > 0 || mode == service.NativeCLI
		})
		if refresh {
			a.refreshServices(server, instance)
		}
	})
}

func (a *App) stopNative(r *nativeRun) {
	if r == nil {
		return
	}
	a.debugAction("native_disconnect", r.server+"/"+r.instance)
	r.stopped = true
	r.stoppingService = false
	if r.cancel != nil {
		r.cancel()
	}
	if r.conn != nil {
		_ = r.conn.Close()
	}
}

func nativeRunningText(r *nativeRun) string {
	if r.stoppingService {
		return T("native.stopping_service")
	}
	switch r.mode {
	case service.NativeSession:
		return T("native.session_running")
	default:
		return T("native.running")
	}
}

func nativeExitedText(mode service.NativeMode, code int) string {
	switch mode {
	case service.NativeSession:
		return T("native.session_exited", "code", code)
	default:
		return T("native.exited", "code", code)
	}
}

// nativeRunView is one session. While it runs the top bar ends it; once
// the remote program exited or the connection dropped, the bar itself
// offers what comes next, so an ended terminal never blocks the page.
func (a *App) nativeRunView(c *ui.Context, r *nativeRun, n *nativeState) {
	info := n.info
	gated := false
	if e, ok := valise.As(r.err); ok && e.Code == service.GateCode {
		gated = true
	}
	card(ui.Column(c)).Gap(10).Children(func() {
		a.nativeRunBar(c, r, n, info, gated)
		a.nativeRestoreReport(c, n)
		if r.term != nil {
			terminal.View(c, r.term).Height(360).AutoFocus()
		}
		if r.err != nil {
			if gated {
				ui.Text(c, T("native.backup_needed")).Wrap()
				ui.Button(c, T("native.go_backup")).OnClick(func() { a.openAgent(r.server, r.instance); a.agentTab = tabManage })
				ui.Checkbox(c, &r.acceptRisk, T("native.accept_risk"))
				if r.acceptRisk {
					ui.Button(c, T("native.continue_once")).OnClick(func() { a.startNativeAccepted(r.server, r.instance, r.mode, true, r.stopService) })
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

func (a *App) nativeRunBar(c *ui.Context, r *nativeRun, n *nativeState, info *install.NativeInfo, gated bool) {
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		switch {
		case r.stopped:
			ui.Text(c, T("native.disconnected")).Grow(1)
		case r.running:
			ui.Text(c, nativeRunningText(r)).Grow(1)
			ui.Button(c, T("native.disconnect")).OnClick(func() { a.stopNative(r) })
			return
		case r.code >= 0:
			ui.Text(c, nativeExitedText(r.mode, r.code)).Grow(1)
		default:
			ui.Text(c, T("native.not_started")).Grow(1)
		}
		if !gated {
			paused := n.pausedUnits
			switch {
			case n.restoreBusy:
				ui.Spinner(c)
				muted(ui.Text(c, T("native.restoring_service")))
			case len(paused) > 0:
				ui.PrimaryButton(c, T("native.restore_service")).OnClick(func() { a.restoreNativeServices(r.server, r.instance, paused) })
				ui.Button(c, T("native.reconnect")).OnClick(func() { a.startNative(r.server, r.instance, r.mode, false) })
			default:
				ui.PrimaryButton(c, T("native.reconnect")).OnClick(func() { a.startNative(r.server, r.instance, r.mode, r.stopService) })
			}
			if info != nil && len(info.SessionArgv) > 0 && r.mode != service.NativeSession {
				ui.Button(c, T("native.use_session")).OnClick(func() { a.startNative(r.server, r.instance, service.NativeSession, false) })
			}
			if info != nil && len(info.Argv) > 0 && r.mode != service.NativeCLI {
				ui.Button(c, T("native.use_cli")).OnClick(func() { a.startNative(r.server, r.instance, service.NativeCLI, false) })
			}
		}
		ui.Button(c, T("native.to_prepare")).OnClick(func() { a.closeNative(r) })
	})
}

// closeNative lets go of an ended session and shows the entries again.
func (a *App) closeNative(r *nativeRun) {
	if r.active() || a.native != r {
		return
	}
	if r.termClose != nil {
		_ = r.termClose.Close()
	}
	a.native = nil
}

func (a *App) nativeBanner(c *ui.Context) {
	r := a.native
	if !r.active() {
		return
	}
	banner(c, "busy", bannerText(c, T("native.banner", "server", r.server, "instance", r.instance), ""), func() {
		ui.Button(c, T("native.return")).OnClick(func() { a.navigate("/native/" + r.server + "/" + r.instance) })
		quietButton(c, T("native.disconnect")).OnClick(func() { a.stopNative(r) })
	})
}
