package gui

import (
	"context"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/valise"
	"valise/ui/service"
)

// The wizard's later steps: identity, login, preparing, and the path from
// ~/.ssh/config (docs/UI.md 7.5).

func (a *App) openImport(w *wizard) {
	w.importing, w.hosts, w.hostsErr = true, nil, nil
	a.run(func() {
		hosts, err := a.svc.ImportableHosts()
		a.update(func() { w.hosts, w.hostsErr = hosts, err })
	})
}

// importStep lists the hosts of ~/.ssh/config not added yet; choosing one
// goes straight to preparing it, logging in with the user's own setup.
func (a *App) importStep(c *ui.Context, w *wizard) {
	card(ui.Column(c)).Gap(12).Padding(22).Children(func() {
		ui.Text(c, T("wizard.import.title")).FontSize(20).Bold().Font(cur.Fonts.Display)
		muted(ui.Text(c, T("wizard.import.sub"))).Wrap()
		switch {
		case w.hostsErr != nil:
			errorBox(c, w.hostsErr, a.fold("wiz-hosts"))
		case w.hosts == nil:
			ui.Spinner(c)
		case len(w.hosts) == 0:
			muted(ui.Text(c, T("wizard.import.none"))).FontSize(13)
		}
		for _, h := range w.hosts {
			ui.Row(c.Key(h)).Gap(10).AlignItems(ui.Center).Padding(8, 0).Children(func() {
				ui.Icon(c, icons.server).FontSize(16).TextColor(palette.TextSub)
				mono(ui.Text(c, h)).Grow(1)
				ui.Button(c, T("wizard.import.go")).OnClick(func() { a.runImport(w, h) })
			})
		}
		a.porterChoice(c, w)
		ui.Row(c).Gap(8).Children(func() {
			ui.Button(c, T("wizard.import.back")).OnClick(func() { w.importing = false })
		})
	})
}

func (a *App) identityStep(c *ui.Context, w *wizard) {
	srv := w.server()
	card(ui.Column(c)).Gap(14).Padding(22).Children(func() {
		ui.Text(c, T("wizard.identity.title")).FontSize(20).Bold().Font(cur.Fonts.Display)
		muted(ui.Text(c, T("wizard.identity.sub"))).FontSize(13).Wrap()
		ui.Column(c).Gap(6).Padding(12, 14).Radius(10).Background(palette.Board).Children(func() {
			muted(ui.Text(c, T("wizard.identity.offered", "addr", knownHostsName(srv.HostName, srv.Port))))
			for _, k := range w.keys {
				ui.Row(c.Key(k.Fingerprint)).Gap(12).Children(func() {
					ui.Text(c, strings.ToUpper(strings.TrimPrefix(k.Type, "ssh-"))).FontSize(12).Bold().Width(90)
					mono(ui.Text(c, k.Fingerprint)).FontSize(12).Wrap().Grow(1)
				})
			}
		})
		ui.Text(c, T("wizard.identity.how")).
			FontSize(12).Wrap()
		if w.mismatch {
			errorBox(c, valise.New("host_key_rejected", T("wizard.identity.stopped"), T("wizard.identity.stopped_hint")), a.fold("wiz-mm"))
		}
		if w.err != nil {
			errorBox(c, w.err, a.fold("wiz-err"))
		}
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			ui.Button(c, T("common.back")).OnClick(func() { w.step, w.err, w.mismatch = wzConnect, nil, false })
			if w.mismatch {
				return
			}
			ui.Button(c, T("wizard.identity.mismatch")).OnClick(func() { w.mismatch = true })
			if w.busy {
				ui.Spinner(c)
				return
			}
			ui.PrimaryButton(c, T("wizard.identity.match")).OnClick(func() { a.trustKeys(w) })
		})
	})
}

func knownHostsName(host string, port int) string {
	if port == 0 {
		port = 22
	}
	return host + ":" + itoa(port)
}

func (a *App) trustKeys(w *wizard) {
	srv, keys := w.server(), w.keys
	w.busy, w.err = true, nil
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := a.svc.TrustHostKeys(ctx, srv, keys)
		a.update(func() {
			w.busy, w.err = false, err
			if err == nil {
				w.step = wzLogin
			}
		})
	})
}

// loginStep says what the login will do, offers the admin pass, and starts
// adding; OpenSSH's own question then comes up as a dialog.
func (a *App) loginStep(c *ui.Context, w *wizard) {
	srv := w.server()
	card(ui.Column(c)).Gap(14).Padding(22).Children(func() {
		ui.Text(c, T("wizard.login.title", "host", srv.HostName)).FontSize(20).Bold().Font(cur.Fonts.Display)
		how := T("wizard.login.how_password")
		if w.login != "password" {
			how = T("wizard.login.how_key")
		}
		ui.Text(c, how).FontSize(13).Wrap()
		passLine(c, T("pass.user"), T("wizard.login.user_pass"))
		want, useValise := w.admin == "yes", a.hasKey()
		if ui.Checkbox(c, &want, adminChoiceLabel(useValise)).Changed() {
			w.admin = map[bool]string{true: "yes"}[want]
		}
		muted(ui.Text(c, T("wizard.login.admin_why"))).Wrap()
		if want && !useValise {
			labeled(c, T("pass.admin_passphrase"), func() {
				ui.TextInput(c, &w.adminPass).Password().Label(T("pass.admin_passphrase")).Placeholder(T("pass.admin_rule"))
			})
			labeled(c, T("pass.again"), func() {
				ui.TextInput(c, &w.adminPass2).Password().Label(T("pass.again")).Placeholder(T("pass.again_hint"))
			})
			muted(ui.Text(c, T("wizard.login.forgot"))).Wrap()
		}
		a.porterChoice(c, w)
		why := ""
		switch {
		case want && !useValise && len([]rune(w.adminPass)) < 12:
			why = T("pass.err.short")
		case want && !useValise && w.adminPass != w.adminPass2:
			why = T("pass.err.differ")
		}
		if why != "" && w.adminPass != "" {
			ui.Text(c, why).FontSize(12).TextColor(palette.Warn)
		}
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			ui.Button(c, T("common.cancel")).OnClick(func() { a.closeWizard() })
			b := ui.PrimaryButton(c, T("wizard.login.go")).OnClick(func() { a.runAdd(w) })
			if why != "" {
				b.Disabled(true)
			}
		})
	})
}

func adminChoiceLabel(useValise bool) string {
	if useValise {
		return T("wizard.login.admin_valise")
	}
	return T("wizard.login.admin_too")
}

func passLine(c *ui.Context, title, sub string) {
	ui.Row(c.Key(title)).Gap(10).Padding(10, 12).Radius(10).Background(palette.Board).Children(func() {
		ui.Text(c, title).Bold().Width(96)
		muted(ui.Text(c, sub)).Wrap().Grow(1)
	})
}

// releaseTag is this client's own release, "" for a development build.
func (a *App) releaseTag() string {
	v := a.svc.Version
	if v == "" || strings.Contains(v, "dev") {
		return ""
	}
	return "v" + strings.TrimPrefix(v, "v")
}

func (a *App) deploySource(w *wizard) service.DeploySource {
	if w.localPorter != "" {
		return service.DeploySource{LocalPath: w.localPorter}
	}
	return service.DeploySource{ReleaseTag: a.releaseTag()}
}

// porterChoice says where porter comes from when the server has none: the
// release of this client's version, or under 更多选项 a local build.
func (a *App) porterChoice(c *ui.Context, w *wizard) {
	ui.Column(c).Gap(6).Children(func() {
		muted(ui.Text(c, T("porter.what"))).Wrap()
		switch tag := a.releaseTag(); {
		case w.localPorter != "":
			ui.Text(c, T("porter.local_chosen")).FontSize(12)
			mono(muted(ui.Text(c, w.localPorter))).Wrap()
		case tag != "":
			ui.Text(c, T("porter.from_release", "tag", tag)).FontSize(12).Wrap()
		default:
			ui.Text(c, T("porter.dev_build")).FontSize(12).Wrap()
		}
		ui.Collapsible(c, T("common.more"), &w.more, func() {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				muted(ui.Text(c, T("porter.local_what"))).Grow(1)
				ui.Button(c, T("common.pick_file")).OnClick(func() { a.pickPorter(w) })
				if w.localPorter != "" {
					ui.Button(c, T("porter.back_release")).OnClick(func() { w.localPorter = "" })
				}
			})
		})
	})
}

func (a *App) pickPorter(w *wizard) {
	if a.pickFile == nil {
		return
	}
	a.run(func() {
		p, err := a.pickFile(T("porter.pick_title"), T("porter.pick_msg"))
		a.update(func() {
			if err == nil && p != "" {
				w.localPorter = p
			}
		})
	})
}

// runAdd adds the server: the passes, then preparing it, step by step.
func (a *App) runAdd(w *wizard) {
	srv := w.server()
	opts := service.AddServerOptions{Server: srv, Setup: service.SetupOptions{Name: srv.Name, Deploy: a.deploySource(w), Doing: a.doingFor(w)}}
	want, useValise := w.admin == "yes", a.hasKey()
	if want && !useValise {
		opts.AdminPassphrase = w.adminPass
	}
	w.adminPass, w.adminPass2 = "", ""
	a.prepare(w, srv.Name, func(ctx context.Context, step func(service.SetupStep)) error {
		if want && useValise {
			pass, err := a.svc.ValisePassphrase(srv.Name)
			if err != nil {
				return err
			}
			opts.AdminPassphrase = pass
		}
		opts.Setup.Step = step
		return a.svc.AddServer(ctx, opts)
	})
}

func (a *App) runImport(w *wizard, alias string) {
	opts := service.SetupOptions{Name: defaultName(alias), Deploy: a.deploySource(w), Doing: a.doingFor(w)}
	a.prepare(w, alias, func(ctx context.Context, step func(service.SetupStep)) error {
		opts.Step = step
		return a.svc.SetupServer(ctx, alias, opts)
	})
}

// prepare runs one way of adding and shows its steps as they come.
func (a *App) prepare(w *wizard, host string, add func(context.Context, func(service.SetupStep)) error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	w.step, w.host, w.busy, w.err, w.steps, w.cancel = wzPrepare, host, true, nil, nil, cancel
	w.doing, w.doingAt = T("prepare.started"), time.Now()
	a.run(func() {
		defer cancel()
		err := add(ctx, func(s service.SetupStep) { a.update(func() { w.steps = append(w.steps, s) }) })
		if ctx.Err() == context.Canceled {
			err = valise.New("cancelled", T("prepare.stopped"), T("prepare.stopped_hint"))
		}
		a.update(func() {
			w.busy, w.err, w.cancel, w.doing = false, err, nil, ""
			a.refreshServers()
			if err != nil {
				return
			}
			w.adminPass, w.adminPass2 = "", ""
			a.refreshAgents()
			if w.fromHome {
				a.router.Push("/servers/" + host + "/agents")
			}
		})
	})
}

// doingFor shows the service's “what is happening now” on the wizard.
func (a *App) doingFor(w *wizard) func(step, what string) {
	return func(_, what string) { a.update(func() { w.doing, w.doingAt = what, time.Now() }) }
}

func (a *App) closeWizard() {
	if a.wiz != nil && a.wiz.cancel != nil {
		a.wiz.cancel()
	}
	a.wiz = nil
	a.router.Push("/servers")
}
