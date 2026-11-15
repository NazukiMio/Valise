package gui

import (
	"context"
	"slices"
	"time"

	"github.com/egoist/mygo/ui"
	"valise/core/environment"
)

type restoreModeState struct {
	view                    *environment.Settings
	draft                   environment.Mode
	j                       job
	started, saved, editing bool
	planSupported           bool
	prepareSupported        bool
	cleanupSupported        bool
}

func (a *App) readRestoreMode(host string, ps *protState) {
	if ps.ref == nil {
		return
	}
	if ps.restoreMode == nil {
		ps.restoreMode = &restoreModeState{}
	}
	m := ps.restoreMode
	if m.j.busy {
		return
	}
	m.started, m.saved = true, false
	ref := *ps.ref
	var v environment.Settings
	var cleanup bool
	a.start(&m.j, time.Minute, func(ctx context.Context) (err error) {
		v, err = a.svc.GetEnvironmentSettings(ctx, host, ref)
		if err == nil {
			caps, e := a.svc.BackupCapabilities(ctx, host)
			cleanup = e == nil && slices.Contains(caps, environment.SwitchCapability)
		}
		return err
	}, func(err error) {
		if err == nil {
			m.view, m.draft = &v, v.Mode
			m.cleanupSupported = cleanup
		}
	})
}

func (a *App) restoreModeCard(c *ui.Context, row *agentRow, ps *protState) {
	if ps.restoreMode == nil {
		ps.restoreMode = &restoreModeState{}
	}
	m := ps.restoreMode
	if !m.started {
		a.readRestoreMode(row.Server, ps)
	}
	card(ui.Column(c)).Gap(10).Children(func() {
		ui.Text(c, T("environment.settings")).Bold()
		if m.j.busy {
			a.jobLine(c, &m.j)
		}
		if m.j.err != nil {
			errorBox(c, m.j.err, a.fold("environment-settings-"+row.Server+row.Instance))
		}
		if m.view == nil {
			if !m.j.busy {
				ui.Button(c, T("common.refresh")).OnClick(func() { a.readRestoreMode(row.Server, ps) })
			}
			return
		}
		fact(c, T("environment.saved_mode"), T("environment.mode."+string(m.view.Mode)))
		if m.view.Busy {
			muted(ui.Text(c, T("environment.busy"))).Wrap()
		} else if !m.j.busy && m.editing {
			ui.RadioGroup(c, func() {
				for _, mode := range []environment.Mode{environment.Parallel, environment.Compact, environment.Direct} {
					value := string(m.draft)
					if ui.Radio(c.Key(string(mode)), &value, string(mode), T("environment.mode."+string(mode))).Changed() {
						m.draft, m.saved = mode, false
					}
					muted(ui.Text(c, T("environment.hint."+string(mode)))).Wrap()
				}
			}).Label(T("environment.settings"))
		}
		if m.editing {
			muted(ui.Text(c, T("environment.save_effect"))).Wrap()
		}
		if m.view.Mode != environment.Direct {
			muted(ui.Text(c, T("environment.parallel_pending"))).Wrap()
		}
		if m.saved {
			ui.Text(c, T("environment.saved"))
		}
		if m.cleanupSupported && m.view.Managed && m.view.Mode == environment.Direct && !m.j.busy {
			muted(ui.Text(c, T("environment.settings_cleanup_effect"))).Wrap()
			ui.Button(c, T("environment.old_cleanup_review")).OnClick(func() { a.reviewSettingsOldCleanup(row.Server, ps) })
		}
		if !m.j.busy {
			ui.Row(c).Gap(10).Children(func() {
				ui.Button(c, T("common.refresh")).OnClick(func() { a.readRestoreMode(row.Server, ps) })
				if !m.view.Busy && !m.editing {
					ui.Button(c, T("environment.edit")).OnClick(func() { m.editing, m.saved = true, false })
				}
				if m.editing {
					ui.Button(c, T("common.cancel")).OnClick(func() { m.draft, m.editing = m.view.Mode, false })
				}
				if m.editing && !m.view.Busy && m.draft != m.view.Mode {
					ui.PrimaryButton(c, T("environment.save")).OnClick(func() { a.needKey(func() { a.saveRestoreMode(row.Server, ps) }) })
				}
			})
		}
	})
}

func (a *App) saveRestoreMode(host string, ps *protState) {
	m := ps.restoreMode
	if m == nil || m.view == nil || m.j.busy || m.view.Busy {
		return
	}
	current, mode := *m.view, m.draft
	var v environment.Settings
	a.start(&m.j, time.Minute, func(ctx context.Context) (err error) {
		v, err = a.svc.SaveEnvironmentSettings(ctx, host, current, mode)
		return err
	}, func(err error) {
		if err == nil {
			m.view, m.draft, m.saved = &v, v.Mode, true
			m.editing = false
			if r := a.restore; r != nil && r.origin == host && r.point.RecordedInstanceID == current.Agent.InstanceID {
				r.restoreMode = nil
			}
		}
	})
}
