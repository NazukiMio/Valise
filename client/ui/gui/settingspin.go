package gui

import (
	"context"
	"strconv"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/ui/service"
)

func (a *App) pinRow(c *ui.Context) {
	on := a.svc.PINEnabled()
	state, action := T("pin.off"), T("pin.enable")
	if on {
		state, action = T("pin.on"), T("pin.change")
	}
	settingRow(c, T("pin.title"), state, "", func() {
		ui.Button(c, action).OnClick(func() { a.keyFlow = &keyFlow{step: kfPIN} })
		if on {
			ui.Button(c, T("pin.disable")).OnClick(func() { a.keyFlow = &keyFlow{step: kfPINOff} })
		}
	})
}

func (a *App) windowRow(c *ui.Context) {
	if a.svc.PINEnabled() {
		settingRow(c, T("window.title"), T("pin.each_operation"), "", nil)
		return
	}
	minutes := int(a.svc.AdminWindow().Minutes())
	settingRow(c, T("window.title"), T("window.current", "n", minutes), "", func() {
		ui.Button(c, T("window.change")).OnClick(func() {
			a.keyFlow = &keyFlow{step: kfUnlockWindow, pass: strconv.Itoa(minutes)}
		})
	})
}

func (a *App) pinStep(c *ui.Context, kf *keyFlow) {
	title := T("pin.enable")
	if a.svc.PINEnabled() {
		title = T("pin.change")
	}
	dialogTitle(ui.Text(c, title))
	muted(ui.Text(c, T("pin.explain"))).Wrap()
	labeled(c, T("key.passphrase"), func() {
		ui.TextInput(c, &kf.old).Password().Label(T("key.passphrase")).AutoFocus()
	})
	labeled(c, T("pin.field"), func() {
		ui.TextInput(c, &kf.pass).Password().Label(T("pin.field"))
	})
	labeled(c, T("pin.again"), func() {
		ui.TextInput(c, &kf.pass2).Password().Label(T("pin.again"))
	})
	why := ""
	switch {
	case kf.old == "", kf.pass == "", kf.pass2 == "":
		why = " "
	case !service.ValidPIN(kf.pass):
		why = T("pin.format")
	case kf.pass != kf.pass2:
		why = T("pass.err.differ")
	}
	a.keyButtons(c, kf, T("pin.save"), why, func() {
		pass, pin := kf.old, kf.pass
		kf.old, kf.pass, kf.pass2 = "", "", ""
		a.saveUnlockSetting(kf, T("pin.on"), func() error { return a.svc.EnablePIN(pass, pin) })
	})
}

func (a *App) pinOffStep(c *ui.Context, kf *keyFlow) {
	dialogTitle(ui.Text(c, T("pin.disable")))
	muted(ui.Text(c, T("pin.disable_why"))).Wrap()
	a.keyButtons(c, kf, T("pin.disable_confirm"), "", func() {
		a.saveUnlockSetting(kf, T("pin.off"), a.svc.DisablePIN)
	})
}

func (a *App) windowStep(c *ui.Context, kf *keyFlow) {
	dialogTitle(ui.Text(c, T("window.title")))
	muted(ui.Text(c, T("window.range"))).Wrap()
	ui.TextInput(c, &kf.pass).Label(T("window.title")).AutoFocus()
	n, err := strconv.Atoi(kf.pass)
	why := ""
	if err != nil || n < 1 || n > 240 {
		why = T("window.range")
	}
	a.keyButtons(c, kf, T("pin.save"), why, func() {
		a.saveUnlockSetting(kf, T("window.current", "n", n), func() error { return a.svc.SetAdminWindow(n) })
	})
}

func (a *App) saveUnlockSetting(kf *keyFlow, note string, save func() error) {
	a.start(&kf.j, time.Minute, func(context.Context) error { return save() }, func(err error) {
		if err == nil {
			a.keyFlow = nil
			a.keyNote = note
		}
	})
}
