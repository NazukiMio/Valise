package gui

import (
	"context"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/client/profiles"
	"valise/ui/service"
)

type genericEnrollState struct {
	host     string
	name     string
	stateDir string
	unit     string
	cli      string
	j        job
	preview  *service.EnrollResult
}

func (a *App) openGenericEnroll(host string) {
	a.genericEnroll = &genericEnrollState{host: host}
}

func (a *App) genericEnrollFn() func(context.Context, string, profiles.GenericProfileInput, bool) (*service.EnrollResult, error) {
	if a.enrollGenericFn != nil {
		return a.enrollGenericFn
	}
	return a.svc.EnrollGeneric
}

func (a *App) genericEnrollModal(c *ui.Context, host string) {
	g := a.genericEnroll
	if g == nil || g.host != host {
		return
	}
	open := true
	if ui.Modal(c, &open, func() {
		dialog(ui.Column(c), 540).Children(func() {
			dialogTitle(ui.Text(c, T("onserver.generic.title")))
			muted(ui.Text(c, T("onserver.generic.sub"))).FontSize(fsSub).Wrap()

			var ch1, ch2, ch3, ch4 bool
			labeled(c, T("onserver.generic.name"), func() {
				ch1 = ui.TextInput(c, &g.name).Label(T("onserver.generic.name")).Placeholder(T("onserver.generic.name_hint")).Disabled(g.j.busy).Changed()
			})
			labeled(c, T("onserver.generic.state_dir"), func() {
				ch2 = ui.TextInput(c, &g.stateDir).Label(T("onserver.generic.state_dir")).Placeholder("~/.myagent").Disabled(g.j.busy).Changed()
			})
			labeled(c, T("onserver.generic.unit"), func() {
				ch3 = ui.TextInput(c, &g.unit).Label(T("onserver.generic.unit")).Placeholder(T("onserver.generic.unit_hint")).Disabled(g.j.busy).Changed()
			})
			labeled(c, T("onserver.generic.cli"), func() {
				ch4 = ui.TextInput(c, &g.cli).Label(T("onserver.generic.cli")).Placeholder(T("onserver.generic.cli_hint")).Disabled(g.j.busy).Changed()
			})
			if ch1 || ch2 || ch3 || ch4 {
				g.preview = nil
				g.j.err = nil
			}

			if g.j.busy {
				a.jobLine(c, &g.j)
				return
			}
			if g.j.err != nil {
				errorBox(c, g.j.err, a.fold("generic-enroll-err-"+host))
			}
			if g.preview != nil && g.preview.Report != nil {
				rep := g.preview.Report
				subPanel(ui.Column(c)).Gap(6).Children(func() {
					fact(c, T("onserver.generic.instance"), rep.InstanceID)
					fact(c, T("onserver.generic.files"), T("onserver.generic.files_val", "n", rep.Unclassified.Files))
					notice(c, "warn", T("onserver.generic.no_secrets_warn"))
				})
			}
			ready := strings.TrimSpace(g.name) != "" && strings.TrimSpace(g.stateDir) != ""
			ui.Row(c).Gap(8).AlignItems(ui.Center).Wrap().Justify(ui.End).Children(func() {
				ui.Button(c, T("common.cancel")).OnClick(func() { a.genericEnroll = nil })
				if g.preview == nil {
					ui.PrimaryButton(c, T("onserver.generic.scan")).Disabled(!ready).OnClick(func() {
						a.runGenericEnroll(g, false)
					})
				} else {
					ui.Button(c, T("onserver.generic.rescan")).Disabled(!ready).OnClick(func() {
						a.runGenericEnroll(g, false)
					})
					ui.PrimaryButton(c, T("onserver.generic.confirm")).OnClick(func() {
						a.runGenericEnroll(g, true)
					})
				}
			})
		})
	}).Dismissed() && !g.j.busy {
		a.genericEnroll = nil
	}
}

func (a *App) runGenericEnroll(g *genericEnrollState, accept bool) {
	if g == nil || g.j.busy {
		return
	}
	in := profiles.GenericProfileInput{
		DisplayName: g.name,
		StateDir:    g.stateDir,
		SystemdUnit: g.unit,
		NativeCLI:   g.cli,
	}
	fn := a.genericEnrollFn()
	var res *service.EnrollResult
	a.start(&g.j, 2*time.Minute, func(ctx context.Context) error {
		var err error
		res, err = fn(ctx, g.host, in, accept)
		return err
	}, func(err error) {
		if err != nil {
			return
		}
		if !accept {
			g.preview = res
			return
		}
		host := g.host
		a.genericEnroll = nil
		if a.enrollGenericFn == nil {
			a.loadHarness()
			a.scanServer(host)
			a.refreshAgents()
		}
	})
}
