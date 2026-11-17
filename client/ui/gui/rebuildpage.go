package gui

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/ui/service"
)

// Forgetting the admin passphrase (docs/ARCHITECTURE.md 3.3): every admin
// pass is rebuilt under a new one; each server's result on its own line.

type rebuildState struct {
	pass, pass2 string
	results     map[string]service.AdminRebuild
	j           job
	started     bool
}

func (a *App) rebuildPage(c *ui.Context) {
	if a.rebuild == nil {
		a.rebuild = &rebuildState{results: map[string]service.AdminRebuild{}}
	}
	rs := a.rebuild
	backLink(c, T("nav.settings"), "/settings")
	card(ui.Column(c)).Gap(12).Padding(22).Children(func() {
		ui.Text(c, T("rebuild.title")).FontSize(20).Bold().Font(cur.Fonts.Display)
		muted(ui.Text(c, T("rebuild.what"))).FontSize(13).Wrap()
		if !rs.started {
			kf := &keyFlow{pass: rs.pass, pass2: rs.pass2}
			why := passPair(c, kf, T("admin.new"))
			rs.pass, rs.pass2 = kf.pass, kf.pass2
			muted(ui.Text(c, T("rebuild.login"))).Wrap()
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				ui.Button(c, T("common.cancel")).OnClick(func() { a.rebuild = nil; a.router.Push("/settings") })
				b := ui.PrimaryButton(c, T("rebuild.go")).OnClick(func() { a.runRebuild(rs) })
				if why != "" {
					b.Disabled(true)
				}
			})
			return
		}
		for _, host := range a.svc.AdminPassServers() {
			a.rebuildLine(c.Key("rb-"+host), rs, host)
		}
		if rs.j.busy {
			ui.Row(c).Justify(ui.End).Children(func() { ui.Button(c, T("common.stop")).OnClick(func() { a.stopJob(&rs.j) }) })
			return
		}
		if rs.j.err != nil {
			errorBox(c, rs.j.err, a.fold("rb-err"))
		}
		ui.Row(c).Justify(ui.End).Children(func() {
			ui.PrimaryButton(c, T("common.done")).OnClick(func() { a.rebuild = nil; a.router.Push("/settings") })
		})
	})
}

func (a *App) rebuildLine(c *ui.Context, rs *rebuildState, host string) {
	r, ok := rs.results[host]
	mark, ink, state := "○", palette.TextSub, T("rebuild.waiting")
	switch {
	case ok && r.Done:
		mark, ink, state = "✓", palette.Ok, T("rebuild.done")
	case ok:
		mark, ink, state = "✗", palette.Warn, T("rebuild.pending")
	case rs.j.busy && a.firstUnfinished(rs) == host:
		mark, ink, state = "▸", palette.Text, a.doingText(rs.j.doing, rs.j.doingAt, c.Now())
	}
	ui.Column(c).Gap(4).Padding(8, 12).Radius(8).Background(palette.Board).Children(func() {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, mark).Bold().TextColor(ink).Width(16)
			ui.Text(c, host).Bold().Width(160)
			muted(ui.Text(c, state)).Grow(1).Wrap()
			if mark == "▸" {
				ui.Spinner(c)
			}
		})
		if ok && r.Err != nil {
			errorBox(c, r.Err, a.fold("rb-err-"+host))
		}
	})
}

func (a *App) firstUnfinished(rs *rebuildState) string {
	for _, h := range a.svc.AdminPassServers() {
		if _, ok := rs.results[h]; !ok {
			return h
		}
	}
	return ""
}

func (a *App) runRebuild(rs *rebuildState) {
	pass := rs.pass
	rs.pass, rs.pass2, rs.started = "", "", true
	a.start(&rs.j, 30*time.Minute, func(ctx context.Context) error {
		a.says(&rs.j)(T("rebuild.logging_in"))
		return a.svc.RebuildAdminPasses(ctx, pass, func(r service.AdminRebuild) {
			a.update(func() { rs.results[r.Server] = r })
			a.says(&rs.j)(T("rebuild.logging_in"))
		})
	}, nil)
}
