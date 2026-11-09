package gui

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/ui/service"
)

// The settings page (docs/UI.md 9.7, 7.8): the key and passphrases, and
// how backups run. One line each; the reasons are under 查看详情.

func (a *App) settingsPage(c *ui.Context) {
	pageHead(c, T("nav.settings"), "")
	ui.Tabs(c, &a.setTab, T("settings.tab.keys"), T("settings.tab.backup"))
	if a.keyNote != "" {
		ui.Row(c).Gap(10).Padding(10, 14).Radius(10).Background(palette.Board).AlignItems(ui.Center).Children(func() {
			ui.Text(c, a.keyNote).TextColor(palette.Ok).Grow(1).Wrap()
			ui.Button(c, T("common.close")).OnClick(func() { a.keyNote = "" })
		})
	}
	if a.setTab == 1 {
		a.backupSettings(c)
		return
	}
	a.keySettings(c)
}

func (a *App) keySettings(c *ui.Context) {
	key, err := a.svc.KeyShow()
	if err != nil {
		card(ui.Column(c)).Gap(10).Children(func() {
			ui.Text(c, T("key.none_title")).FontSize(16).Bold()
			muted(ui.Text(c, T("key.none_what"))).FontSize(13).Wrap()
			ui.Row(c).Gap(8).Children(func() {
				ui.PrimaryButton(c, T("key.set_open")).OnClick(func() { a.keyFlow = &keyFlow{step: kfSet, lock: true} })
				ui.Button(c, T("key.import")).OnClick(func() { a.keyFlow = &keyFlow{step: kfImport} })
			})
		})
		return
	}
	kb := a.svc.KeyBackupState()
	card(ui.Column(c)).Gap(8).Children(func() {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icons.key).FontSize(24).TextColor(palette.Brand)
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				ui.Text(c, T("key.state_title")).FontSize(16).Bold()
				mono(muted(ui.Text(c, T("key.fingerprint", "fp", key.Fingerprint))))
			})
			if kb.ExportedAt != nil {
				plainTag(c, T("key.backed_up", "when", when(*kb.ExportedAt, c.Now())))
			} else {
				ui.Text(c, T("key.not_backed_up")).FontSize(12).Bold().TextColor(palette.Warn)
			}
		})
		ui.Collapsible(c, T("common.details"), a.fold("key-details"), func() {
			muted(ui.Text(c, T("key.details", "path", key.Path))).Wrap()
		})
	})
	card(ui.Column(c)).Gap(8).Children(func() {
		settingRow(c, T("key.row.backup"), T("key.row.backup_now"), "", func() {
			ui.Button(c, T("key.export_open")).OnClick(func() { a.keyFlow = &keyFlow{step: kfGate, choice: "export"} })
		})
		settingRow(c, T("key.row.passphrase"), T("key.row.passphrase_now"), "", func() {
			ui.Button(c, T("key.change_open")).OnClick(func() { a.keyFlow = &keyFlow{step: kfChange} })
		})
		a.lockRow(c)
		a.adminRow(c)
	})
	if a.lockOff {
		a.lockOffDialog(c)
	}
}

// lockRow is the backup lock: on with how many servers can be used, or
// off. The cost is one line; the rest is on the recovery page.
func (a *App) lockRow(c *ui.Context) {
	if !a.svc.RecoveryEnabled() {
		settingRow(c, T("lock.title"), T("lock.off"), "", func() {
			ui.Button(c, T("lock.on_open")).OnClick(func() { a.keyFlow = &keyFlow{step: kfLockOn} })
		})
		return
	}
	n := 0
	for _, l := range a.svc.ServerLocks() {
		if l.State == service.LockDone {
			n++
		}
	}
	settingRow(c, T("lock.title"), T("lock.on", "n", n), T("lock.cost"), func() {
		ui.Button(c, T("lock.recover")).OnClick(func() { a.router.Push("/settings/recover") })
		ui.Button(c, T("lock.off_open")).OnClick(func() { a.lockOff = true })
	})
}

func (a *App) lockOffDialog(c *ui.Context) {
	if ui.Modal(c, &a.lockOff, func() {
		ui.Column(c).Width(420).Padding(24).Gap(12).Children(func() {
			ui.Text(c, T("lock.off_title")).FontSize(18).Bold()
			muted(ui.Text(c, T("lock.off_what"))).FontSize(13).Wrap()
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				ui.Button(c, T("common.cancel")).OnClick(func() { a.lockOff = false })
				ui.PrimaryButton(c, T("lock.off_go")).OnClick(func() {
					if err := a.svc.DisableRecovery(); err == nil {
						a.lockOff = false
					}
				})
			})
		})
	}).Dismissed() {
		a.lockOff = false
	}
}

func (a *App) adminRow(c *ui.Context) {
	servers := a.svc.AdminPassServers()
	if len(servers) == 0 {
		settingRow(c, T("admin.title"), T("admin.none"), "", nil)
		return
	}
	pending := 0
	for _, s := range servers {
		if a.svc.AdminRebuildPending(s) {
			pending++
		}
	}
	now := T("admin.servers", "n", len(servers), "m", int(a.svc.AdminWindow().Minutes()))
	note := ""
	if pending > 0 {
		note = T("admin.pending", "n", pending)
	}
	settingRow(c, T("admin.title"), now, note, func() {
		ui.Button(c, T("admin.change_open")).OnClick(func() { a.keyFlow = &keyFlow{step: kfAdminChange} })
		ui.Button(c, T("admin.forgot")).OnClick(func() { a.router.Push("/settings/admin-rebuild") })
	})
}

func (a *App) backupSettings(c *ui.Context) {
	card(ui.Column(c)).Gap(8).Children(func() {
		on := a.svc.Config.DetachedTasks
		settingRow(c, T("detach.title"), T("detach.what"), "", func() {
			if ui.Switch(c, &on).Label(T("detach.title")).Changed() {
				_ = a.svc.SetDetachedTasks(on)
			}
		})
	})
}

// lockOnStep turns the backup lock on: it needs the Valise passphrase.
func (a *App) lockOnStep(c *ui.Context, kf *keyFlow) {
	ui.Text(c, T("lock.on_title")).FontSize(18).Bold()
	muted(ui.Text(c, T("lock.cost"))).FontSize(13).Wrap()
	labeled(c, T("key.passphrase"), func() {
		ui.TextInput(c, &kf.pass).Password().Label(T("key.passphrase")).AutoFocus()
	})
	why := ""
	if kf.pass == "" {
		why = " "
	}
	a.keyButtons(c, kf, T("lock.on_go"), why, func() {
		pass := kf.pass
		a.start(&kf.j, time.Minute, func(context.Context) error { _, err := a.svc.EnableRecovery(pass); return err },
			func(err error) {
				if err == nil {
					a.keyFlow = nil
				}
			})
	})
}

// adminChangeStep changes the admin passphrase; every admin pass is
// sealed again.
func (a *App) adminChangeStep(c *ui.Context, kf *keyFlow) {
	ui.Text(c, T("admin.change_title")).FontSize(18).Bold()
	labeled(c, T("admin.current"), func() {
		ui.TextInput(c, &kf.old).Password().Label(T("admin.current")).AutoFocus()
	})
	why := passPair(c, kf, T("admin.new"))
	a.keyButtons(c, kf, T("admin.change_go"), why, func() {
		old, pass := kf.old, kf.pass
		a.start(&kf.j, time.Minute, func(ctx context.Context) error {
			_, err := a.svc.ChangeAdminPassphrase(ctx, old, pass)
			return err
		}, func(err error) {
			if err == nil {
				a.keyFlow = nil
				a.keyNote = T("admin.changed")
			}
		})
	})
}
