package gui

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/ui/service"
)

// Finding the Valise passphrase again (docs/ARCHITECTURE.md 3.2, UI.md
// 7.8): pick a server with a backup lock, enter recovery mode, check
// porter, run the sudo command there, type the code back, set a new
// passphrase.

type recoverState struct {
	host  string // "" while choosing; set means recovery mode
	check *service.PorterCheck
	run   *service.RecoveryRun
	code  string
	ok    bool // the code was right
	pass  string
	pass2 string
	done  *service.RecoveryDone
	j     job
	more  bool
}

func (a *App) recovering() bool { return a.recover != nil && a.recover.host != "" }

// recoveryBanner stands over every page in recovery mode; only this page
// is shown until it ends.
func (a *App) recoveryBanner(c *ui.Context) {
	if !a.recovering() {
		return
	}
	if a.router.Path() != "/settings/recover" {
		a.router.Push("/settings/recover")
	}
	var actions func()
	if a.recover.done == nil {
		actions = func() { ui.Button(c, T("recover.cancel")).OnClick(func() { a.endRecovery() }) }
	}
	banner(c, "warn", bannerText(c, T("recover.mode"), T("recover.mode_what")), actions)
}

func (a *App) endRecovery() {
	rs := a.recover
	if rs != nil && rs.run != nil && rs.done == nil {
		run := rs.run
		a.run(func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			a.svc.CancelRecovery(ctx, run)
		})
	}
	a.stopJob(&rs.j)
	a.svc.SetRecoveryMode(false)
	a.recover = nil
	a.router.Push("/settings")
}

func (a *App) recoverPage(c *ui.Context) {
	if a.recover == nil {
		a.recover = &recoverState{}
	}
	rs := a.recover
	if !a.recovering() {
		backLink(c, T("nav.settings"), "/settings")
		a.lockList(c, rs)
		return
	}
	card(ui.Column(c)).Gap(12).Padding(22).Children(func() {
		ui.Text(c, T("recover.title", "host", rs.host)).FontSize(20).Bold().Font(cur.Fonts.Display)
		switch {
		case rs.done != nil:
			a.recoverDone(c, rs)
		case rs.ok:
			a.recoverNewPass(c, rs)
		case rs.run != nil:
			a.recoverCommand(c, rs)
		default:
			a.recoverCheck(c, rs)
		}
	})
}

// lockList is every server's backup lock; only a done one can be used.
func (a *App) lockList(c *ui.Context, rs *recoverState) {
	pageHead(c, T("recover.list_title"), T("recover.list_sub"))
	card(ui.Column(c)).Gap(8).Children(func() {
		for _, l := range a.svc.ServerLocks() {
			now := T("lock.state." + l.State)
			if l.At != nil {
				now += T("common.dot_sep") + when(*l.At, c.Now())
			}
			settingRow(c, l.Server, now, "", func() {
				if l.State == service.LockDone {
					ui.Button(c, T("recover.use")).OnClick(func() { a.beginRecovery(l.Server) })
				}
			})
		}
	})
	ui.Collapsible(c, T("common.details"), &rs.more, func() {
		muted(ui.Text(c, T("recover.details"))).Wrap()
	})
}

// beginRecovery enters recovery mode and checks the server's porter.
func (a *App) beginRecovery(host string) {
	rs := &recoverState{host: host}
	a.recover = rs
	a.svc.SetRecoveryMode(true)
	var pc *service.PorterCheck
	a.start(&rs.j, time.Minute, func(ctx context.Context) (err error) {
		a.says(&rs.j)(T("recover.checking"))
		pc, err = a.svc.CheckPorter(ctx, host)
		return err
	}, func(error) { rs.check = pc })
}

func (a *App) recoverCheck(c *ui.Context, rs *recoverState) {
	if rs.j.busy {
		a.jobLine(c, &rs.j)
		return
	}
	if rs.j.err != nil {
		errorBox(c, rs.j.err, a.fold("rec-err"))
	}
	if pc := rs.check; pc != nil {
		if pc.Match {
			ui.Text(c, T("recover.porter_ok", "version", pc.Version)).TextColor(palette.Ok)
		} else {
			ui.Text(c, T("recover.porter_bad")).Bold().TextColor(palette.Warn)
			muted(ui.Text(c, T("recover.porter_bad_how"))).Wrap()
		}
		ui.Collapsible(c, T("common.details"), &rs.more, func() {
			muted(ui.Text(c, T("recover.scan_why"))).Wrap()
		})
	}
	ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
		if rs.check != nil && !rs.check.Match {
			ui.Button(c, T("recover.to_porter")).OnClick(func() {
				host := rs.host
				a.endRecovery()
				a.serverTab = 1
				a.router.Push("/servers/" + host + "/porter")
			})
			return
		}
		b := ui.PrimaryButton(c, T("recover.request")).OnClick(func() {
			host := rs.host
			var run *service.RecoveryRun
			a.start(&rs.j, time.Minute, func(ctx context.Context) (err error) {
				a.says(&rs.j)(T("recover.placing"))
				run, err = a.svc.StartRecovery(ctx, host)
				return err
			}, func(error) { rs.run = run })
		})
		if rs.check == nil {
			b.Disabled(true)
		}
	})
}

func (a *App) recoverCommand(c *ui.Context, rs *recoverState) {
	ui.Text(c, T("recover.run_this", "host", rs.host)).Bold()
	muted(ui.Text(c, T("recover.run_why"))).Wrap()
	ui.Box(c).Padding(12, 14).Radius(10).Background(palette.Board).Children(func() {
		mono(ui.Text(c, rs.run.Command)).FontSize(12).Wrap()
	})
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		if a.copyText != nil {
			ui.Button(c, T("recover.copy")).OnClick(func() { _ = a.copyText(rs.run.Command) })
		}
		muted(ui.Text(c, T("recover.copy_note"))).Grow(1).Wrap()
	})
	labeled(c, T("recover.code"), func() {
		ui.TextInput(c, &rs.code).Label(T("recover.code")).Placeholder("1234 5678")
	})
	if rs.j.busy {
		a.jobLine(c, &rs.j)
		return
	}
	if rs.j.err != nil {
		errorBox(c, rs.j.err, a.fold("rec-err"))
	}
	ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
		ui.PrimaryButton(c, T("common.next")).OnClick(func() {
			run, code := rs.run, rs.code
			a.start(&rs.j, time.Minute, func(ctx context.Context) error { return a.svc.VerifyRecovery(ctx, run, code) },
				func(err error) {
					rs.ok = err == nil
					if run.Tries >= 3 {
						rs.run, rs.code = nil, ""
					}
				})
		})
	})
}

func (a *App) recoverNewPass(c *ui.Context, rs *recoverState) {
	kf := &keyFlow{pass: rs.pass, pass2: rs.pass2}
	why := passPair(c, kf, T("key.new"))
	rs.pass, rs.pass2 = kf.pass, kf.pass2
	muted(ui.Text(c, T("recover.same_key"))).Wrap()
	if rs.j.busy {
		a.jobLine(c, &rs.j)
		return
	}
	if rs.j.err != nil {
		errorBox(c, rs.j.err, a.fold("rec-err"))
	}
	ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
		b := ui.PrimaryButton(c, T("recover.set")).OnClick(func() {
			run, pass := rs.run, rs.pass
			var done *service.RecoveryDone
			a.start(&rs.j, 2*time.Minute, func(ctx context.Context) (err error) {
				done, err = a.svc.FinishRecovery(ctx, run, pass)
				return err
			}, func(err error) {
				if err == nil {
					rs.done, rs.pass, rs.pass2 = done, "", ""
				}
			})
		})
		if why != "" {
			b.Disabled(true)
		}
	})
}

func (a *App) recoverDone(c *ui.Context, rs *recoverState) {
	ui.Text(c, T("recover.done")).Bold().TextColor(palette.Ok)
	for _, l := range rs.done.Locks {
		passLine(c, l.Server, T("lock.state."+l.State))
	}
	muted(ui.Text(c, T("recover.export_again"))).Wrap()
	ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
		ui.PrimaryButton(c, T("recover.leave")).OnClick(func() {
			a.svc.SetRecoveryMode(false)
			a.recover = nil
			a.router.Push("/settings")
		})
	})
}
