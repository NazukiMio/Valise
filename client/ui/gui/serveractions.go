package gui

import (
	"context"
	"strconv"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/ui/service"
)

// The settings' own pages: renaming, switching to the Valise passes and
// updating porter. Each says what it will change before it does.

// backToSettings is the link back to the server's settings tab.
func (a *App) backToSettings(c *ui.Context, name string) {
	backLink(c, T("set.back", "name", name), "/servers/"+name)
}

// renamePlaces puts the server's own backup library first.
func renamePlaces(plan *service.RenamePlan, done bool) []service.RenamePlace {
	return append([]service.RenamePlace{{Kind: "server", From: plan.Old, To: plan.New, Done: done}}, plan.Places...)
}

func (a *App) renamePage(c *ui.Context, name string) {
	ss := a.settingsOf(name)
	a.backToSettings(c, name)
	card(ui.Column(c)).Gap(14).Padding(22).Children(func() {
		ui.Text(c, T("rename.title", "name", name)).FontSize(20).Bold().Font(cur.Fonts.Display)
		if ss.renamed && ss.plan != nil {
			a.renameResult(c, name, ss)
			return
		}
		labeled(c, T("rename.new"), func() {
			in := ui.TextInput(c, &ss.newName).Label(T("rename.new")).Placeholder(T("set.name_rule")).AutoFocus()
			if in.Changed() {
				ss.plan = nil
			}
		})
		muted(ui.Text(c, T("rename.rule"))).Wrap()
		if ss.planJ.busy {
			a.jobLine(c, &ss.planJ)
		}
		if ss.planJ.err != nil {
			errorBox(c, ss.planJ.err, a.fold("plan-err-"+name))
		}
		if ss.plan != nil {
			ui.Text(c, T("rename.places")).Bold()
			for _, p := range renamePlaces(ss.plan, false) {
				renamePlaceLine(c, p, false)
			}
		}
		if ss.renJ.busy {
			a.jobLine(c, &ss.renJ)
			return
		}
		if ss.renJ.err != nil {
			errorBox(c, ss.renJ.err, a.fold("ren-err-"+name))
		}
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			ui.Button(c, T("common.cancel")).OnClick(func() { a.serverTab = 1; a.router.Push("/servers/" + name) })
			if ss.plan == nil {
				b := ui.PrimaryButton(c, T("rename.list")).OnClick(func() {
					to := ss.newName
					var plan *service.RenamePlan
					a.start(&ss.planJ, time.Minute, func(ctx context.Context) (err error) {
						a.says(&ss.planJ)(T("rename.listing"))
						plan, err = a.svc.PlanRename(ctx, name, to)
						return err
					}, func(err error) { ss.plan = plan })
				})
				if !validName(ss.newName) || ss.planJ.busy {
					b.Disabled(true)
				}
				return
			}
			ui.PrimaryButton(c, T("rename.go")).OnClick(func() {
				plan := ss.plan
				a.start(&ss.renJ, 10*time.Minute, func(ctx context.Context) error {
					a.says(&ss.renJ)(T("rename.running"))
					return a.svc.RenameServer(ctx, plan)
				}, func(err error) {
					ss.renamed = err == nil
					a.readSettings(name)
				})
			})
		})
	})
}

func (a *App) renameResult(c *ui.Context, name string, ss *settingsState) {
	ui.Text(c, T("rename.after")).Bold()
	for _, p := range renamePlaces(ss.plan, true) {
		renamePlaceLine(c, p, true)
	}
	if ss.renJ.err != nil {
		errorBox(c, ss.renJ.err, a.fold("ren-err-"+name))
	}
	ui.Row(c).Justify(ui.End).Children(func() {
		ui.PrimaryButton(c, T("common.done")).OnClick(func() { a.serverTab = 1; a.router.Push("/servers/" + name) })
	})
}

// renamePlaceLine is one place a rename moves, before or after.
func renamePlaceLine(c *ui.Context, p service.RenamePlace, after bool) {
	kind := map[string]string{"server": T("rename.kind.server"), "repo": T("rename.kind.repo"), "station": T("rename.kind.station")}[p.Kind]
	if kind == "" {
		kind = T("rename.kind.other")
	}
	ui.Row(c.Key("rp-"+p.Kind+p.Name)).Gap(12).Padding(10, 12).Radius(10).Background(palette.Board).AlignItems(ui.Center).Children(func() {
		ui.Text(c, kind+" "+p.Name).Bold().Width(170)
		state, ink := "", palette.TextSub
		switch {
		case p.Missing:
			state = T("rename.missing")
		case !after:
			state = p.From + " → " + p.To
		case p.Done:
			state, ink = T("rename.done"), palette.Ok
		case p.Pending:
			state, ink = T("rename.pending"), palette.Warn
		default:
			state, ink = T("rename.not_done"), palette.Warn
		}
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			ui.Text(c, state).TextColor(ink).Wrap()
			if after && p.Err != "" {
				muted(ui.Text(c, p.Err)).Wrap()
			}
		})
	})
}

func (a *App) migratePage(c *ui.Context, name string) {
	ss := a.settingsOf(name)
	a.backToSettings(c, name)
	card(ui.Column(c)).Gap(14).Padding(22).Children(func() {
		ui.Text(c, T("migrate.title", "name", name)).FontSize(20).Bold().Font(cur.Fonts.Display)
		if ss.migrated {
			ui.Text(c, T("migrate.done", "name", name)).TextColor(palette.Ok).Bold()
			if ss.porterMissing {
				muted(ui.Text(c, T("migrate.porter_missing"))).Wrap()
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ss.porterMissing {
					ui.Button(c, T("set.porter_deploy")).OnClick(func() { a.router.Push("/servers/" + name + "/porter") })
				}
				ui.PrimaryButton(c, T("common.done")).OnClick(func() { a.serverTab = 1; a.router.Push("/servers/" + name) })
			})
			return
		}
		ui.Text(c, T("migrate.will")).Bold()
		for i, k := range []string{"migrate.step.login", "migrate.step.user", "migrate.step.check", "migrate.step.hostkey"} {
			passLine(c, strconv.Itoa(i+1), T(k, "name", name))
		}
		want, useValise := ss.migAdmin == "yes", a.hasKey()
		if ui.Checkbox(c, &want, adminChoiceLabel(useValise)).Changed() {
			ss.migAdmin = map[bool]string{true: "yes"}[want]
		}
		if want && !useValise {
			labeled(c, T("pass.admin_passphrase"), func() {
				ui.TextInput(c, &ss.migPass).Password().Label(T("pass.admin_passphrase")).Placeholder(T("pass.admin_rule"))
			})
			labeled(c, T("pass.again"), func() {
				ui.TextInput(c, &ss.migPass2).Password().Label(T("pass.again")).Placeholder(T("pass.again_hint"))
			})
		}
		muted(ui.Text(c, T("migrate.after", "name", name))).Wrap()
		if ss.migJ.busy {
			a.jobLine(c, &ss.migJ)
			return
		}
		if ss.migJ.err != nil {
			errorBox(c, ss.migJ.err, a.fold("mig-err-"+name))
		}
		bad := want && !useValise && (len([]rune(ss.migPass)) < 12 || ss.migPass != ss.migPass2)
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			ui.Button(c, T("common.cancel")).OnClick(func() { ss.migPass, ss.migPass2 = "", ""; a.router.Push("/servers/" + name) })
			b := ui.PrimaryButton(c, T("migrate.go")).OnClick(func() {
				pass := ""
				if want && !useValise {
					pass = ss.migPass
				}
				ss.migPass, ss.migPass2 = "", ""
				var missing bool
				a.start(&ss.migJ, 10*time.Minute, func(ctx context.Context) (err error) {
					a.says(&ss.migJ)(T("migrate.running", "name", name))
					if want && useValise {
						pass, err = a.svc.ValisePassphrase(name)
						if err != nil {
							return err
						}
					}
					missing, err = a.svc.MigrateServer(ctx, name, pass)
					return err
				}, func(err error) {
					ss.migrated, ss.porterMissing = err == nil, missing
					a.refreshServers()
					a.readDoctor(name)
				})
			})
			if bad {
				b.Disabled(true)
			}
		})
	})
}

func (a *App) porterPage(c *ui.Context, name string) {
	ss := a.settingsOf(name)
	ps := a.passOf(name)
	a.backToSettings(c, name)
	card(ui.Column(c)).Gap(14).Padding(22).Children(func() {
		ui.Text(c, T("porter.update_title", "name", name)).FontSize(20).Bold().Font(cur.Fonts.Display)
		ui.Row(c).Gap(10).Children(func() {
			on := T("set.unknown")
			if ps.doc != nil && ps.doc.PorterVersion != "" {
				on = ps.doc.PorterVersion
			}
			tile(c, T("porter.on_server"), on)
			tile(c, T("porter.here"), orText(a.svc.Version, T("set.unknown")))
			if ps.doc != nil && ps.doc.Platform.OS != "" {
				tile(c, T("porter.arch"), ps.doc.Platform.OS+"/"+ps.doc.Platform.Arch)
			}
		})
		if r := ss.deployed; r != nil {
			ui.Text(c, T("porter.updated", "version", r.Porter.Info.Version)).TextColor(palette.Ok).Bold()
			mono(muted(ui.Text(c, "SHA-256 "+r.SHA256))).Wrap()
			ui.Row(c).Justify(ui.End).Children(func() {
				ui.PrimaryButton(c, T("common.done")).OnClick(func() { a.serverTab = 1; a.router.Push("/servers/" + name) })
			})
			return
		}
		if ss.porterW == nil {
			ss.porterW = &wizard{}
		}
		w := ss.porterW
		a.porterChoice(c, w)
		muted(ui.Text(c, T("porter.records_sha"))).Wrap()
		muted(ui.Text(c, T("porter.not_resident"))).Wrap()
		if ss.deployJ.busy {
			a.jobLine(c, &ss.deployJ)
			return
		}
		if ss.deployJ.err != nil {
			errorBox(c, ss.deployJ.err, a.fold("dep-err-"+name))
		}
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			ui.Button(c, T("common.cancel")).OnClick(func() { a.serverTab = 1; a.router.Push("/servers/" + name) })
			src := a.deploySource(w)
			b := ui.PrimaryButton(c, T("porter.update_go")).OnClick(func() {
				var rep *service.DeployReport
				a.start(&ss.deployJ, 10*time.Minute, func(ctx context.Context) (err error) {
					a.says(&ss.deployJ)(T("porter.uploading"))
					rep, err = a.svc.DeployPorter(ctx, name, src)
					return err
				}, func(err error) {
					ss.deployed = rep
					a.readDoctor(name)
				})
			})
			if src.LocalPath == "" && src.ReleaseTag == "" {
				b.Disabled(true)
			}
		})
	})
}

// tile is one fact in a small box.
func tile(c *ui.Context, label, value string) {
	ui.Column(c.Key("tile-"+label)).Gap(4).Padding(12, 14).Radius(10).Background(palette.Board).Grow(1).Children(func() {
		muted(ui.Text(c, label))
		ui.Text(c, value).Bold()
	})
}
