package gui

import (
	"strconv"

	"github.com/egoist/mygo/ui"

	"valise/client/config"
	"valise/ui/service"
)

// Removed servers (docs/ARCHITECTURE.md 2.3): folded away at the bottom of
// the servers page, each with how it was removed and a way to add it
// again; their backups stay and are tagged where they show.

func (a *App) removedSection(c *ui.Context) {
	if len(a.removed) == 0 {
		return
	}
	ui.Collapsible(c, T("removed.title", "n", len(a.removed)), a.fold("removed-servers"), func() {
		card(ui.Column(c)).Gap(8).Children(func() {
			for _, r := range a.removed {
				ui.Row(c.Key("rm-"+r.Name)).Gap(12).Padding(8, 4).AlignItems(ui.Center).Children(func() {
					ui.Column(c).Grow(1).Gap(3).Children(func() {
						ui.Text(c, r.Name).Bold().TextColor(palette.TextSub)
						how := T("removed.here_only")
						if r.Cleaned {
							how = T("removed.cleaned")
						}
						muted(ui.Text(c, T("removed.line", "when", when(r.RemovedAt, c.Now()), "how", how))).Wrap()
					})
					plainTag(c, T("removed.tag"))
					if r.Server != nil && r.Server.Valise() {
						srv := *r.Server
						ui.Button(c, T("removed.add_again")).OnClick(func() { a.wizardFor(srv, wzConnect) })
					}
					ui.Button(c, T("removed.forget")).OnClick(func() { a.forgetting, a.forgetErr = r.Name, nil })
				})
			}
		})
	})
	if a.forgetting != "" {
		a.forgetDialog(c)
	}
}

// forgetDialog confirms dropping a removed server's record; the backups
// stay where they are.
func (a *App) forgetDialog(c *ui.Context) {
	open := true
	name := a.forgetting
	if ui.Modal(c, &open, func() {
		dialog(ui.Column(c), 440).Children(func() {
			dialogTitle(ui.Text(c, T("removed.forget_title", "name", name)))
			muted(ui.Text(c, T("removed.forget_what"))).FontSize(13).Wrap()
			if a.forgetErr != nil {
				errorBox(c, a.forgetErr, a.fold("forget-err"))
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				ui.Button(c, T("common.cancel")).OnClick(func() { a.forgetting = "" })
				ui.PrimaryButton(c, T("removed.forget_go")).OnClick(func() {
					if a.forgetErr = a.svc.ForgetRemoved(name); a.forgetErr == nil {
						a.forgetting = ""
						a.refreshServers()
					}
				})
			})
		})
	}).Dismissed() {
		a.forgetting = ""
	}
}

// rekeyServer opens the add wizard at the login step for a server already
// in the list: logging in once with the server password installs a new
// user pass for this computer (docs/ARCHITECTURE.md 3.1 rekey).
func (a *App) rekeyServer(name string) {
	list, err := a.svc.ServerList()
	if err != nil {
		return
	}
	for _, s := range list {
		if s.Name == name {
			a.wizardFor(s, wzLogin)
		}
	}
}

// wizardFor opens the add wizard filled in from a server record.
func (a *App) wizardFor(srv config.Server, step int) {
	w := &wizard{login: srv.Login, name: srv.Name, addr: srv.HostName, user: srv.User, keyPath: srv.LoginKey, step: step}
	if w.login == "" {
		w.login = config.LoginPassword
	}
	if srv.Port != 0 && srv.Port != 22 {
		w.port = strconv.Itoa(srv.Port)
	}
	a.wiz = w
	a.router.Push("/servers/add")
}

// removedRows are the removed servers' lines in a repository on the
// backup locations page, tagged; nothing is copied to them any more.
func (a *App) removedRepoLines(c *ui.Context, repo string) {
	for _, r := range a.removed {
		var st service.RepoStatus
		for _, s := range a.svc.RepoStatuses(r.Name, orText(r.ServerName, r.Name)) {
			if s.Name == repo {
				st = s
			}
		}
		n := 0
		for _, snaps := range st.Leaves {
			n += len(snaps)
		}
		if n == 0 && st.State.LastOK == nil {
			continue
		}
		ui.Row(c.Key("rmrepo-"+r.Name)).Gap(12).Padding(12, 14).Radius(10).Background(palette.Board).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icons.server).FontSize(16).TextColor(palette.TextSub)
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, orText(r.ServerName, r.Name)).FontSize(14).Bold().TextColor(palette.TextSub)
					plainTag(c, T("removed.tag"))
				})
				muted(ui.Text(c, T("removed.repo_line", "n", n, "state", repoStateLine(st, c.Now())))).Wrap()
			})
		})
	}
}
