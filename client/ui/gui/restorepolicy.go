package gui

import (
	"context"
	"slices"
	"time"

	"github.com/egoist/mygo/ui"
	"valise/core/environment"
)

func directRestoreReady(m *restoreModeState) bool {
	return m != nil && !m.j.busy && m.j.err == nil && m.view != nil && !m.view.Managed && !m.view.Busy && m.view.Mode == environment.Direct
}

func (a *App) readRestorePolicy(r *restoreFlow) {
	if r.restoreMode == nil {
		r.restoreMode = &restoreModeState{}
	}
	m := r.restoreMode
	if m.j.busy {
		return
	}
	m.started = true
	var v environment.Settings
	var supported, preparation bool
	a.start(&m.j, time.Minute, func(ctx context.Context) (err error) {
		v, err = a.svc.InstanceEnvironmentSettings(ctx, r.origin, r.point.RecordedInstanceID)
		if err == nil {
			caps, e := a.svc.BackupCapabilities(ctx, r.origin)
			err = e
			supported = e == nil && slices.Contains(caps, environment.PlanCapability)
			preparation = e == nil && slices.Contains(caps, environment.PreparationCapability)
		}
		return err
	}, func(err error) {
		if err == nil {
			m.view, m.draft = &v, v.Mode
			m.planSupported = supported
			m.prepareSupported = preparation
		}
	})
}

func (a *App) restorePolicyPanel(c *ui.Context, r *restoreFlow) {
	if r.restoreMode == nil || !r.restoreMode.started {
		a.readRestorePolicy(r)
	}
	m := r.restoreMode
	card(ui.Column(c)).Gap(8).Children(func() {
		ui.Text(c, T("environment.settings")).Bold()
		if m.j.busy {
			a.jobLine(c, &m.j)
		}
		if m.j.err != nil {
			errorBox(c, m.j.err, a.fold("restore-policy-error"))
		}
		if m.view != nil {
			fact(c, T("environment.saved_mode"), T("environment.mode."+string(m.view.Mode)))
			muted(ui.Text(c, T("environment.hint."+string(m.view.Mode)))).Wrap()
			if m.view.Busy {
				ui.Text(c, T("environment.busy")).Wrap()
			}
			if m.view.Managed || m.view.Mode != environment.Direct {
				ui.Text(c, T("environment.parallel_pending")).Wrap()
			}
		}
		ui.Row(c).Gap(10).Children(func() {
			ui.Button(c, T("environment.open_settings")).OnClick(func() {
				a.openAgent(r.origin, r.point.RecordedInstanceID)
				a.agentTab = tabManage
			})
			if !m.j.busy {
				ui.Button(c, T("common.refresh")).OnClick(func() { a.readRestorePolicy(r) })
			}
		})
	})
}
