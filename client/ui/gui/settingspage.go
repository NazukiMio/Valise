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
	ui.Tabs(c, &a.setTab, T("settings.tab.keys"), T("settings.tab.backup"), T("settings.tab.personalization"), T("settings.tab.profiles"))
	if a.keyNote != "" {
		ui.Row(c).Gap(10).Padding(10, 14).Radius(10).Background(palette.Board).AlignItems(ui.Center).Children(func() {
			ui.Text(c, a.keyNote).TextColor(palette.Ok).Grow(1).Wrap()
			ui.Button(c, T("common.close")).OnClick(func() { a.keyNote = "" })
		})
	}
	switch a.setTab {
	case 3:
		a.profilesSettings(c)
	case 2:
		a.personalization(c)
	case 1:
		a.backupSettings(c)
		ui.Button(c, T("restore.history")).OnClick(func() { a.readRestoreTasks(); a.navigate("/restore/tasks") })
	default:
		a.keySettings(c)
		a.configTransferCard(c)
	}
	a.debugSettings(c)
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
		if len(a.svc.AdminPassServers()) > 0 {
			card(ui.Column(c)).Gap(8).Children(func() {
				a.windowRow(c)
				a.adminRow(c)
			})
		}
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
		a.passphraseRow(c)
		a.pinRow(c)
		a.windowRow(c)
		a.adminRow(c)
	})
	if a.lockOff {
		a.lockOffDialog(c)
	}
}

// passphraseRow is the Valise passphrase with its backup lock: what
// finding a forgotten passphrase needs, so the lock is a button of this
// row, and 忘了口令 is there only while the lock is on.
func (a *App) passphraseRow(c *ui.Context) {
	on := a.svc.RecoveryEnabled()
	now := T("lock.off_now")
	if on {
		n := 0
		for _, l := range a.svc.ServerLocks() {
			if l.State == service.LockDone {
				n++
			}
		}
		now = T("lock.on", "n", n)
	}
	settingRow(c, T("key.row.passphrase"), now, "", func() {
		ui.Button(c, T("key.change_open")).OnClick(func() { a.keyFlow = &keyFlow{step: kfChange} })
		if !on {
			ui.Button(c, T("lock.on_open")).OnClick(func() { a.keyFlow = &keyFlow{step: kfLockOn} })
			return
		}
		ui.Button(c, T("lock.recover")).OnClick(func() { a.router.Push("/settings/recover") })
		ui.Button(c, T("lock.off_open")).OnClick(func() { a.lockOff = true })
	})
}

func (a *App) lockOffDialog(c *ui.Context) {
	if ui.Modal(c, &a.lockOff, func() {
		dialog(ui.Column(c), 420).Children(func() {
			dialogTitle(ui.Text(c, T("lock.off_title")))
			muted(ui.Text(c, T("lock.off_what"))).FontSize(13).Wrap()
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				ui.Button(c, T("common.cancel")).OnClick(func() { a.lockOff = false })
				dangerButton(c, T("lock.off_go")).OnClick(func() {
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
	now := T("admin.servers", "n", len(servers))
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
	dialogTitle(ui.Text(c, T("lock.on_title")))
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
	dialogTitle(ui.Text(c, T("admin.change_title")))
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
