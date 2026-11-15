package gui

import (
	"github.com/egoist/mygo/ui"
	"time"
	"valise/core/enroll"
)

func (a *App) currentRuntime(c *ui.Context, v *enroll.RuntimeState, at time.Time, reading bool) {
	if v == nil {
		return
	}
	card(ui.Column(c)).Padding(16).Gap(8).Children(func() {
		ui.Text(c, T("runtime.title")).Bold().FontSize(14)
		if reading {
			muted(ui.Text(c, T("runtime.refreshing"))).FontSize(12).Wrap()
			if !at.IsZero() {
				muted(ui.Text(c, T("runtime.read_at", "time", at.Format("2006-01-02 15:04:05")))).FontSize(11).Wrap()
			}
		}
		if !v.Known {
			key := "runtime.unverified"
			if v.IssueCode == "environment_binding_changing" {
				key = "runtime.changing"
			}
			muted(ui.Text(c, T(key))).Wrap()
			return
		}
		ui.Text(c, T("runtime.slot", "slot", v.Slot)).FontSize(13).Wrap()
		running := "runtime.run_unknown"
		if v.Running {
			running = "runtime.running"
		} else if v.Stopped {
			running = "runtime.stopped"
		}
		ui.Text(c, T(running)).FontSize(13).Wrap()
		gateway := "runtime.gateway_pending"
		if v.Running && v.PortOwned && v.HTTPResponded && v.GatewayPassed {
			gateway = "runtime.gateway_passed"
		}
		ui.Text(c, T(gateway)).FontSize(13).Wrap()
		kind := "runtime.managed"
		if v.Native {
			kind = "runtime.native"
		}
		muted(ui.Text(c, T(kind))).FontSize(12).Wrap()
		confirmation := "runtime.conversation_pending"
		if v.ConversationConfirmed {
			confirmation = "runtime.conversation_confirmed"
		}
		muted(ui.Text(c, T(confirmation))).FontSize(12).Wrap()
	})
}
