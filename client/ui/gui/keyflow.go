package gui

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"
)

// The key's dialogs (docs/UI.md 9.1, 9.7, 7.8): set the Valise passphrase
// the first time it is needed, back the key up before the first backup,
// import a recovery kit, change the passphrase. Passphrases typed here
// live only until the dialog closes.

const (
	kfSet         = "set"
	kfGate        = "gate"
	kfImport      = "import"
	kfChange      = "change"
	kfLockOn      = "lock_on"
	kfAdminChange = "admin_change"
)

type keyFlow struct {
	step             string
	old, pass, pass2 string
	lock             bool   // make the backup lock too
	dir              string // kit folder
	choice           string // gate: "export" or "later"
	j                job
	then             func() // what waited for the key
	more             bool
}

// needKey runs then once a key exists and, before the first backup, was
// backed up or deliberately left for later.
func (a *App) needKey(then func()) {
	switch {
	case !a.hasKey():
		a.keyFlow = &keyFlow{step: kfSet, lock: true, then: then}
	case !a.svc.KeyBackupDecided():
		a.keyFlow = &keyFlow{step: kfGate, choice: "export", then: then}
	default:
		then()
	}
}

func (a *App) hasKey() bool { _, err := a.svc.KeyShow(); return err == nil }

func (a *App) keyDialog(c *ui.Context) {
	kf := a.keyFlow
	if kf == nil {
		return
	}
	open := true
	if ui.Modal(c, &open, func() {
		ui.Column(c).Width(460).Padding(24).Gap(12).Children(func() {
			switch kf.step {
			case kfSet:
				a.setPassStep(c, kf)
			case kfGate:
				a.gateStep(c, kf)
			case kfImport:
				a.kitImportStep(c, kf)
			case kfChange:
				a.changeStep(c, kf)
			case kfLockOn:
				a.lockOnStep(c, kf)
			case kfAdminChange:
				a.adminChangeStep(c, kf)
			}
		})
	}).Dismissed() && !kf.j.busy {
		a.keyFlow = nil
	}
}

// passPair is a passphrase typed twice; it says what is wrong, "" when fine.
func passPair(c *ui.Context, kf *keyFlow, label string) string {
	labeled(c, label, func() {
		ui.TextInput(c, &kf.pass).Password().Label(label).Placeholder(T("key.rule")).AutoFocus()
	})
	labeled(c, T("pass.again"), func() {
		ui.TextInput(c, &kf.pass2).Password().Label(T("pass.again")).Placeholder(T("pass.again_hint"))
	})
	switch {
	case len([]rune(kf.pass)) < 12:
		return T("pass.err.short")
	case kf.pass != kf.pass2:
		return T("pass.err.differ")
	}
	return ""
}

func (a *App) keyButtons(c *ui.Context, kf *keyFlow, label, why string, do func()) {
	if kf.j.busy {
		a.jobLine(c, &kf.j)
		return
	}
	if kf.j.err != nil {
		errorBox(c, kf.j.err, a.fold("key-err"))
	}
	if why != "" && kf.pass != "" {
		ui.Text(c, why).FontSize(12).TextColor(palette.Warn)
	}
	ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
		ui.Button(c, T("common.cancel")).OnClick(func() { a.keyFlow = nil })
		b := ui.PrimaryButton(c, label).OnClick(do)
		if why != "" {
			b.Disabled(true)
		}
	})
}

func (a *App) setPassStep(c *ui.Context, kf *keyFlow) {
	ui.Text(c, T("key.set_title")).FontSize(18).Bold()
	muted(ui.Text(c, T("key.set_what"))).FontSize(13).Wrap()
	why := passPair(c, kf, T("key.passphrase"))
	ui.Checkbox(c, &kf.lock, T("key.lock_too"))
	ui.Collapsible(c, T("common.details"), &kf.more, func() {
		muted(ui.Text(c, T("key.lock_details"))).Wrap()
	})
	a.keyButtons(c, kf, T("key.set_go"), why, func() {
		pass, lock := kf.pass, kf.lock
		a.start(&kf.j, time.Minute, func(context.Context) error {
			if _, err := a.svc.KeyInit(pass); err != nil {
				return err
			}
			if lock {
				_, err := a.svc.EnableRecovery(pass)
				return err
			}
			return nil
		}, func(err error) {
			if err == nil {
				kf.pass, kf.pass2, kf.step = "", "", kfGate
				kf.choice = "export"
			}
		})
	})
	ui.Row(c).Gap(6).Children(func() {
		muted(ui.Text(c, T("key.have_one")))
		ui.Button(c, T("key.import")).OnClick(func() { kf.step, kf.pass, kf.pass2 = kfImport, "", "" })
	})
}

func (a *App) gateStep(c *ui.Context, kf *keyFlow) {
	ui.Text(c, T("key.gate_title")).FontSize(18).Bold()
	muted(ui.Text(c, T("key.gate_what"))).FontSize(13).Wrap()
	ui.RadioGroup(c, func() {
		ui.Radio(c, &kf.choice, "export", T("key.export"))
		if kf.choice == "export" {
			a.folderField(c, kf)
		}
		ui.Radio(c, &kf.choice, "later", T("key.later"))
		if kf.choice == "later" {
			muted(ui.Text(c, T("key.later_what"))).Wrap().Padding(0, 0, 0, 26)
		}
	}).Label(T("key.gate_title"))
	why := ""
	if kf.choice == "export" && kf.dir == "" {
		why = " "
	}
	label := T("key.export_go")
	if kf.choice == "later" {
		label = T("key.later_go")
	}
	a.keyButtons(c, kf, label, why, func() {
		choice, dir, then := kf.choice, kf.dir, kf.then
		a.start(&kf.j, time.Minute, func(context.Context) error {
			if choice == "later" {
				return a.svc.BackUpKeyLater()
			}
			_, err := a.svc.ExportKit(dir)
			return err
		}, func(err error) {
			if err == nil {
				a.keyFlow = nil
				if then != nil {
					then()
				}
			}
		})
	})
}

// folderField picks a folder with the system dialog.
func (a *App) folderField(c *ui.Context, kf *keyFlow) {
	ui.Row(c).Gap(8).Padding(0, 0, 0, 26).AlignItems(ui.Center).Children(func() {
		mono(muted(ui.Text(c, orText(kf.dir, T("key.no_folder"))))).Grow(1).Wrap()
		ui.Button(c, T("key.pick_folder")).OnClick(func() {
			if a.pickFolder == nil {
				return
			}
			a.run(func() {
				p, err := a.pickFolder()
				a.update(func() {
					if err == nil && p != "" {
						kf.dir = p
					}
				})
			})
		})
	})
}

func (a *App) kitImportStep(c *ui.Context, kf *keyFlow) {
	ui.Text(c, T("key.import_title")).FontSize(18).Bold()
	a.folderField(c, kf)
	labeled(c, T("key.passphrase_of_kit"), func() {
		ui.TextInput(c, &kf.pass).Password().Label(T("key.passphrase_of_kit"))
	})
	why := ""
	if kf.dir == "" || kf.pass == "" {
		why = " "
	}
	a.keyButtons(c, kf, T("key.import_go"), why, func() {
		dir, pass, then := kf.dir, kf.pass, kf.then
		var fp string
		a.start(&kf.j, time.Minute, func(context.Context) error {
			info, err := a.svc.ImportKit(dir, pass)
			if err == nil {
				fp = info.Fingerprint
			}
			return err
		}, func(err error) {
			if err == nil {
				a.keyNote = T("key.imported", "fp", fp)
				// The kit it came from is a backup of the key.
				a.svc.KitImportedFrom(dir)
				a.keyFlow = nil
				if then != nil {
					then()
				}
			}
		})
	})
}

func (a *App) changeStep(c *ui.Context, kf *keyFlow) {
	ui.Text(c, T("key.change_title")).FontSize(18).Bold()
	labeled(c, T("key.current"), func() {
		ui.TextInput(c, &kf.old).Password().Label(T("key.current")).AutoFocus()
	})
	why := passPair(c, kf, T("key.new"))
	muted(ui.Text(c, T("key.change_what"))).Wrap()
	a.keyButtons(c, kf, T("key.change_go"), why, func() {
		old, pass := kf.old, kf.pass
		a.start(&kf.j, time.Minute, func(context.Context) error { return a.svc.ChangePassphrase(old, pass) }, func(err error) {
			if err == nil {
				a.keyFlow = nil
				a.keyNote = T("key.changed")
			}
		})
	})
}
