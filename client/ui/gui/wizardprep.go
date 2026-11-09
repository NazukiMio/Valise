package gui

import (
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/ui/service"
)

// prepLabels name the steps of preparing a server, in order: adding by
// hand has the passes and the last check; importing from ~/.ssh/config
// records the server instead.
func prepLabels(importing bool) [][2]string {
	if importing {
		return [][2]string{{service.SetupCheck, T("prepare.check")}, {service.SetupDeploy, T("prepare.porter")},
			{service.SetupRegister, T("prepare.register")}, {service.SetupName, T("prepare.name")}, {service.SetupRecord, T("prepare.record")}}
	}
	return [][2]string{{service.SetupKeys, T("prepare.keys")}, {service.SetupCheck, T("prepare.check")}, {service.SetupDeploy, T("prepare.porter")},
		{service.SetupRegister, T("prepare.register")}, {service.SetupName, T("prepare.name")}, {service.SetupDaily, T("prepare.daily")}}
}

// stepDetail says a finished step's result in the interface's words.
func stepDetail(s service.SetupStep) string {
	if s.Step == service.SetupKeys {
		out := T("prepare.user_pass")
		if strings.Contains(s.Detail, "admin") {
			out += T("common.list_sep") + T("pass.admin")
		}
		return out
	}
	return s.Detail
}

// prepareStep shows each step on its own line: done, running, failed or
// not reached, said apart (docs/UI.md 7.5).
func (a *App) prepareStep(c *ui.Context, w *wizard) {
	card(ui.Column(c)).Gap(12).Padding(22).Children(func() {
		ui.Text(c, T("prepare.title", "host", w.host)).FontSize(20).Bold().Font(cur.Fonts.Display)
		done := map[string]service.SetupStep{}
		for _, s := range w.steps {
			done[s.Step] = s
		}
		next := true
		for _, l := range prepLabels(w.importing) {
			s, ok := done[l[0]]
			mark, ink, detail := "○", palette.TextSub, ""
			switch {
			case ok:
				mark, ink, detail = "✓", palette.Ok, stepDetail(s)
			case next && w.busy:
				mark, ink, detail, next = "▸", palette.Text, a.doingNow(w, c.Now()), false
			case next && w.err != nil:
				mark, ink, detail, next = "✗", palette.Warn, T("prepare.failed_step"), false
			}
			ui.Row(c.Key(l[0])).Gap(12).Padding(8, 12).Radius(8).Background(palette.Board).AlignItems(ui.Center).Children(func() {
				ui.Text(c, mark).Bold().TextColor(ink).Width(16)
				ui.Text(c, l[1]).Bold().Width(170)
				muted(ui.Text(c, detail)).Wrap().Grow(1)
				if mark == "▸" {
					ui.Spinner(c)
				}
			})
		}
		if w.err != nil {
			errorBox(c, w.err, a.fold("wiz-err"))
			a.porterChoice(c, w)
		}
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			switch {
			case w.busy:
				muted(ui.Text(c, T("prepare.can_stop"))).Grow(1)
				ui.Button(c, T("common.stop")).OnClick(func() { a.stopPrepare(w) })
			case w.err != nil:
				ui.Button(c, T("common.cancel")).OnClick(func() { a.closeWizard() })
				ui.PrimaryButton(c, T("common.retry")).OnClick(func() {
					if w.importing {
						a.runImport(w, w.host)
					} else {
						a.runAdd(w)
					}
				})
			default:
				ui.Button(c, T("common.done")).OnClick(func() { a.closeWizard() })
				ui.PrimaryButton(c, T("wizard.next.agents")).OnClick(func() { a.router.Push("/servers/" + w.host + "/agents") })
			}
		})
	})
}

// doingNow is the step's current sub-step with how long it has waited, or
// that OpenSSH is waiting for the user's answer in the dialog.
func (a *App) doingNow(w *wizard, now time.Time) string {
	for _, p := range a.asks {
		if p.kind == askServer {
			return T("prepare.ssh_asking")
		}
	}
	text := orText(w.doing, T("prepare.running"))
	if secs := int(now.Sub(w.doingAt).Seconds()); !w.doingAt.IsZero() && secs >= 3 {
		return T("prepare.waited", "what", text, "n", secs)
	}
	return T("prepare.doing", "what", text)
}

// stopPrepare ends the running work and the questions it asked.
func (a *App) stopPrepare(w *wizard) {
	if w.cancel != nil {
		w.cancel()
	}
	a.dropServerPrompts()
}

// dropServerPrompts answers OpenSSH's open questions with “cancelled” when
// the user stops: the ssh asking is being ended anyway.
func (a *App) dropServerPrompts() {
	keep := a.asks[:0]
	for _, p := range a.asks {
		if p.kind == askServer {
			p.reply <- passReply{err: errStopped}
			continue
		}
		keep = append(keep, p)
	}
	a.asks = keep
}
