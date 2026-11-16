package gui

import (
	"context"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/ui/service"
)

// The fix built into Valise that the desktop offers: turning linger on
// (docs/UI.md 9.5). The plan is read first; its confirmation lists every
// command and the system setting it changes; the result keeps each exit
// code apart from the re-check (AGENTS.md 7.5). The management login is a
// fresh one: the admin passphrase, or the server password asked in this
// window.

type sysFixState struct {
	item    service.HealthItem
	loading bool // reading the server and planning
	fix     *service.SystemFix
	confirm bool // the confirmation is open
	running bool
	result  *service.SystemFixResult
	err     error
}

func (f *sysFixState) busy() bool { return f.loading || f.running }

// prepareSysFix plans the fix; nothing runs before the confirmation.
func (a *App) prepareSysFix(row *agentRow, st *healthState, it service.HealthItem) {
	f := &sysFixState{item: it, loading: true}
	st.sys = f
	server, instance := row.Server, row.Instance
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		fix, err := a.svc.PlanSystemFix(ctx, server, instance, it.SystemFix, service.SystemFixOptions{})
		a.update(func() {
			f.loading, f.fix, f.err = false, fix, err
			f.confirm = err == nil && fix.Mode == service.FixDirect
		})
	})
}

// runSysFix runs the confirmed plan (the digest binds it), then reads
// health again.
func (a *App) runSysFix(st *healthState) {
	f := st.sys
	f.confirm, f.running = false, true
	fix := f.fix
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		res, err := a.svc.RunSystemFix(ctx, fix.Host, fix.Instance, fix.ID, fix.Digest, service.SystemFixOptions{}, service.InstallIO{})
		a.update(func() {
			f.running, f.result, f.err = false, res, err
			a.readHealth(fix.Host, fix.Instance)
			a.refreshAgents()
		})
	})
}

func (a *App) sysFixReport(c *ui.Context, row *agentRow, st *healthState) {
	f := st.sys
	if f.confirm {
		a.sysFixConfirm(c, st)
		return
	}
	if !f.busy() && f.err == nil && f.result == nil && (f.fix == nil || f.fix.Mode == service.FixDirect) {
		st.sys = nil // the confirmation was closed without an answer
		return
	}
	reportPanel(ui.Column(c)).Gap(6).Children(func() {
		switch {
		case f.loading:
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() { ui.Spinner(c); muted(ui.Text(c, T("health.sysfix.planning"))) })
		case f.running:
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c)
				ui.Text(c, T("health.running", "name", f.item.ActionName)).FontSize(13)
			})
		case f.result == nil && f.err != nil:
			errorBox(c, f.err, a.fold("sysfix-err-"+row.Server+row.Instance))
		case f.result == nil:
			sysFixManual(c, f.fix)
		default:
			sysFixResult(c, f.result)
			if f.err != nil {
				errorBox(c, f.err, a.fold("sysfix-err-"+row.Server+row.Instance))
			}
		}
		if !f.busy() {
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if f.err != nil || f.result != nil && (f.result.Verification == nil || f.result.Verification.State != "resolved") {
					ui.Button(c, T("common.retry")).OnClick(func() { a.prepareSysFix(row, st, f.item) })
				}
				ui.Button(c, T("common.close")).OnClick(func() { st.sys = nil })
			})
		}
	})
}

// sysFixConfirm is the second, explicit consent: what the fix is for, then
// each command with what it changes and how to undo it, never folded.
func (a *App) sysFixConfirm(c *ui.Context, st *healthState) {
	f := st.sys
	fix := f.fix
	ui.Modal(c, &f.confirm, func() {
		dialog(ui.Column(c), 500).Children(func() {
			dialogTitle(ui.Text(c, T("health.run_title", "name", fix.Name)))
			muted(ui.Text(c, T("health.sysfix.kind")))
			fact(c, T("health.sysfix.goal"), fix.Goal)
			fact(c, T("health.sysfix.impact"), fix.Impact)
			codeBox(ui.Column(c)).Gap(4).Children(func() {
				muted(ui.Text(c, T("health.sysfix.cmds", "server", fix.Host)))
				for i, step := range fix.Steps {
					ui.Column(c.Key(i)).Gap(4).Children(func() {
						ui.Text(c, strings.Join(step.Command, " ")).FontSize(12).Font(cur.Fonts.Mono).Wrap()
						ui.Text(c, T("health.sysfix.change", "what", step.Change)).FontSize(12).Wrap()
						muted(ui.Text(c, T("health.sysfix.undo", "how", step.Undo))).Wrap()
					})
				}
			})
			muted(ui.Text(c, fix.Access)).Wrap()
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				ui.Button(c, T("common.cancel")).OnClick(func() { st.sys = nil })
				ui.PrimaryButton(c, T("health.run")).OnClick(func() { a.runSysFix(st) })
			})
		})
	})
}

// sysFixManual: Valise cannot run this plan from the window; it says why
// and shows the commands, and runs nothing.
func sysFixManual(c *ui.Context, fix *service.SystemFix) {
	ui.Text(c, T("health.sysfix.manual")).FontSize(13).Bold().TextColor(palette.Warn).Wrap()
	if fix.Why != "" {
		muted(ui.Text(c, fix.Why)).Wrap()
	}
	for i, cmd := range fix.Manual {
		ui.Text(c.Key(i), strings.Join(cmd, " ")).FontSize(12).Font(cur.Fonts.Mono).Selectable()
	}
}

// sysFixResult tells each command's exit and the re-check apart: a command
// that exited 0 has not solved the problem until the re-check says so.
func sysFixResult(c *ui.Context, r *service.SystemFixResult) {
	for i, step := range r.Steps {
		cmd := strings.Join(step.Command, " ")
		line := T("health.sysfix.skipped", "cmd", cmd, "why", step.Note)
		if step.Ran {
			line = T("health.sysfix.exit", "cmd", cmd, "code", step.Exit)
		}
		ui.Text(c.Key(i), line).FontSize(13).Bold().Wrap()
	}
	if v := r.Verification; v != nil {
		tone := map[string]string{"resolved": "ok", "remaining": "warn"}[v.State]
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			dot := ui.Box(c).Size(6, 6).Radius(3).Background(palette.Line)
			if tone != "" {
				dot.Background(toneColor(tone))
			}
			ui.Text(c, T("health.sysfix.recheck", "text", v.Text)).FontSize(13).Wrap()
		})
	}
	if r.Next != "" {
		muted(ui.Text(c, T("health.sysfix.next", "text", r.Next))).Wrap()
	}
	if !r.Audited {
		ui.Text(c, T("health.sysfix.unaudited")).FontSize(12).TextColor(palette.Warn).Wrap()
	}
}
