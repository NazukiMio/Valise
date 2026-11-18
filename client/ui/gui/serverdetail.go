package gui

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/ui/service"
)

// The server detail (docs/UI.md 7.5): its state, porter and source each as
// a tag; the two passes; the agents on it; and why it is offline or needs
// attention.

// passState is what the detail page reads and does for one server.
type passState struct {
	doc         *service.DoctorReport
	docErr      error
	docBusy     bool
	busy        bool // issuing or revoking the admin pass
	err         error
	issuing     bool // the issue form is open
	pass, pass2 string
	revoke      bool // the revoke confirmation is open
}

func (a *App) passOf(host string) *passState {
	ps := a.passes[host]
	if ps == nil {
		ps = &passState{}
		a.passes[host] = ps
		a.readDoctor(host)
	}
	return ps
}

// readDoctor asks porter its version over the user pass.
func (a *App) readDoctor(host string) {
	ps := a.passes[host]
	ps.docBusy, ps.docErr = true, nil
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		doc, err := a.svc.Doctor(ctx, host)
		a.update(func() { ps.docBusy, ps.doc, ps.docErr = false, doc, err })
	})
}

func (a *App) serverPage(c *ui.Context, name string) {
	if !a.servers.started {
		a.refreshServers()
	}
	var row *serverRow
	for i := range a.servers.value {
		if a.servers.value[i].Name == name {
			row = &a.servers.value[i]
		}
	}
	ps := a.passOf(name)
	st := a.serverState(name, c.Now())
	backLink(c, T("nav.servers"), "/servers")
	ui.Row(c).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Children(func() { pageHead(c, name, "") })
		if a.agents.busy {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() { ui.Spinner(c); muted(ui.Text(c, T("server.connecting", "name", name))) })
		} else {
			ui.Button(c, T("server.reconnect")).OnClick(func() { a.refreshAgents(); a.readDoctor(name); a.readSettings(name) })
		}
	})
	ui.Row(c).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
		stateTag(c, st)
		if obs := a.observation(name); obs != nil && obs.ObservedAt != nil {
			muted(ui.Text(c, T("server.read_at", "when", when(*obs.ObservedAt, c.Now()))))
		}
		switch {
		case ps.doc != nil && ps.doc.PorterVersion != "":
			plainTag(c, T("server.porter_version", "version", ps.doc.PorterVersion))
		case ps.doc != nil && ps.doc.Problem != nil:
			plainTag(c, T("server.porter_unusable"))
		}
		if row != nil && row.Valise {
			plainTag(c, T("server.kind.valise"))
		} else if row != nil {
			plainTag(c, T("server.kind.ssh"))
		}
	})
	ui.Tabs(c, &a.serverTab, T("server.tab.overview"), T("server.tab.settings"))
	if a.serverTab == 1 {
		a.settingsTab(c, name, row, ps)
		return
	}
	if st.Key == "offline" || st.Key == "attention" {
		a.serverTrouble(c, name, st)
	}
	if row != nil {
		a.passBlock(c, name, row.Valise, ps)
	}
	a.agentsHere(c, name)
}

// plainTag is a neutral fact as a small tag.
func plainTag(c *ui.Context, s string) {
	ui.Box(c.Key(s)).Padding(2, 10).Radius(999).Border(1, palette.Line).Children(func() {
		ui.Text(c, s).FontSize(12).TextColor(palette.TextSub)
	})
}

func (a *App) serverTrouble(c *ui.Context, name string, st serverState) {
	obs := a.observation(name)
	card(ui.Column(c)).Gap(8).Background(palette.TroubleBg).Border(1, palette.TroubleBorder).Children(func() {
		ui.Text(c, name+" "+st.Word).FontSize(15).Bold().TextColor(palette.Warn)
		fact(c, T("fact.reason"), st.Reason)
		if st.Suggestion != "" {
			fact(c, T("fact.suggestion"), st.Suggestion)
		}
		if obs != nil && !obs.LastAttemptAt.IsZero() {
			fact(c, T("fact.last_try"), when(obs.LastAttemptAt, c.Now()))
		}
		if obs != nil && obs.ObservedAt != nil {
			fact(c, T("fact.last_read"), when(*obs.ObservedAt, c.Now()))
		}
		if obs != nil && obs.Error != nil && obs.Error.Detail != "" {
			ui.Collapsible(c, T("error.tech_info"), a.fold("srv-detail-"+name), func() {
				mono(muted(ui.Text(c, obs.Error.Code+"\n"+obs.Error.Detail))).Wrap()
			})
		}
	})
}

// passBlock is the user pass and the admin pass, or for a server from
// ~/.ssh/config, that the user's own SSH setup logs in.
func (a *App) passBlock(c *ui.Context, name string, tiered bool, ps *passState) {
	card(ui.Column(c)).Gap(10).Children(func() {
		ui.Text(c, T("pass.title")).FontSize(16).Bold()
		if !tiered {
			muted(ui.Text(c, T("pass.own_ssh"))).FontSize(13).Wrap()
			return
		}
		user := T("pass.usable")
		if !a.svc.HasDailyKey(name) {
			user = T("pass.not_here")
		}
		passLine(c, T("pass.user_state", "state", user), T("pass.user_what"))
		has := a.svc.HasAdminKey(name)
		left, open := a.svc.AdminRemaining(name)
		ui.Row(c).Gap(10).Padding(10, 12).Radius(10).Background(palette.Board).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).Gap(3).Children(func() {
				switch {
				case !has:
					ui.Text(c, T("pass.admin_none")).Bold()
					muted(ui.Text(c, T("pass.admin_none_how"))).Wrap()
				case a.svc.PINEnabled():
					ui.Text(c, T("pass.admin_locked")).Bold()
					muted(ui.Text(c, T("pin.each_operation"))).Wrap()
				case open:
					ui.Text(c, T("pass.admin_open", "n", int(left.Minutes())+1)).Bold()
					muted(ui.Text(c, T("pass.admin_auto_lock"))).Wrap()
				default:
					ui.Text(c, T("pass.admin_locked")).Bold()
					muted(ui.Text(c, T("pass.admin_locked_how", "n", int(a.svc.AdminWindow().Minutes())))).Wrap()
				}
			})
			switch {
			case ps.busy:
				ui.Spinner(c)
			case !has && !ps.issuing:
				ui.Button(c, T("pass.issue")).OnClick(func() { ps.issuing, ps.err = true, nil })
			case has:
				ui.Button(c, T("pass.revoke")).OnClick(func() { ps.revoke, ps.err = true, nil })
			}
		})
		if ps.issuing && !ps.busy {
			a.issueForm(c, name, ps)
		}
		if ps.err != nil {
			errorBox(c, ps.err, a.fold("pass-err-"+name))
		}
	})
	if ps.revoke {
		a.revokeDialog(c, name, ps)
	}
}

func (a *App) issueForm(c *ui.Context, name string, ps *passState) {
	ui.Column(c).Gap(8).Children(func() {
		why := ""
		if a.hasKey() {
			muted(ui.Text(c, T("pass.issue_valise"))).Wrap()
		} else {
			kf := &keyFlow{pass: ps.pass, pass2: ps.pass2}
			why = passPair(c, kf, T("pass.admin_passphrase"))
			ps.pass, ps.pass2 = kf.pass, kf.pass2
			muted(ui.Text(c, T("pass.issue_login"))).Wrap()
		}
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			ui.Button(c, T("common.cancel")).OnClick(func() { ps.issuing, ps.pass, ps.pass2 = false, "", "" })
			b := ui.PrimaryButton(c, T("pass.issue_go")).OnClick(func() { a.issuePass(name, ps) })
			if why != "" {
				b.Disabled(true)
			}
		})
	})
}

func (a *App) issuePass(name string, ps *passState) {
	pass, useValise := ps.pass, a.hasKey()
	ps.busy, ps.err, ps.pass, ps.pass2 = true, nil, "", ""
	a.run(func() {
		if a.svc != nil {
			stopOp := a.svc.BeginOperation()
			defer stopOp()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		var err error
		if useValise {
			pass, err = a.svc.ValisePassphrase(name)
		}
		if err == nil {
			err = a.svc.CreateAdminKey(ctx, name, pass)
		}
		a.update(func() {
			ps.busy, ps.err = false, err
			if err == nil {
				ps.issuing = false
			}
		})
	})
}

func (a *App) revokeDialog(c *ui.Context, name string, ps *passState) {
	if ui.Modal(c, &ps.revoke, func() {
		dialog(ui.Column(c), 420).Children(func() {
			dialogTitle(ui.Text(c, T("pass.revoke_title", "name", name)))
			muted(ui.Text(c, T("pass.revoke_why"))).FontSize(12).Wrap()
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				ui.Button(c, T("common.cancel")).OnClick(func() { ps.revoke = false })
				dangerButton(c, T("pass.revoke")).OnClick(func() {
					ps.revoke, ps.busy, ps.err = false, true, nil
					a.run(func() {
						ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
						defer cancel()
						err := a.svc.RemoveAdminKey(ctx, name)
						a.update(func() { ps.busy, ps.err = false, err })
					})
				})
			})
		})
	}).Dismissed() {
		ps.revoke = false
	}
}

// agentsHere lists the agents on the server, each leading to its page.
func (a *App) agentsHere(c *ui.Context, name string) {
	var here []agentRow
	for _, r := range a.agentRows(a.agents.value) {
		if r.Server == name && !r.Retired() {
			here = append(here, r)
		}
	}
	card(ui.Column(c)).Gap(10).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.Text(c, T("server.agents_here", "n", len(here))).FontSize(16).Bold().Grow(1)
			ui.Button(c, T("server.find")).OnClick(func() { a.router.Push("/servers/" + name + "/agents") })
		})
		if len(here) == 0 {
			muted(ui.Text(c, T("server.no_agents"))).FontSize(13)
		}
		for _, r := range here {
			line := pressable(ui.Row(c.Key(r.Instance)).Gap(10).Padding(spInside, spInside).Radius(radControl).AlignItems(ui.Center), r.Name, radControl)
			line.OnClick(func() { a.router.Push("/agents/" + r.Server + "/" + r.Instance) }).Children(func() {
				a.avatar(c, r, 24)
				ui.Text(c, r.Name).Bold()
				muted(ui.Text(c, r.Harness)).Grow(1)
				chevron(c)
			})
		}
	})
}
