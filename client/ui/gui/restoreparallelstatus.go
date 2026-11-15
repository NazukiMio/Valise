package gui

import (
	"github.com/egoist/mygo/ui"
	"valise/core/environment"
)

func (a *App) parallelPreparationPage(c *ui.Context, r *restoreFlow) {
	if !r.parallelRead && r.task.OpID != "" {
		a.parallelPreparationAction(r, "status")
	}
	a.readParallelSwitchCapability(r)
	if r.j.busy {
		if r.parallelPrepared != nil {
			a.restoreProgress(c, r)
		} else {
			a.jobLine(c, &r.j)
		}
		return
	}
	ui.Row(c).Gap(10).Children(func() {
		if r.task.OpID != "" {
			ui.Button(c, T("common.refresh")).OnClick(func() { a.parallelPreparationAction(r, "status") })
		}
		ui.Button(c, T("restore.history")).OnClick(func() { a.readRestoreTasks(); a.navigate("/restore/tasks") })
	})
	if r.task.RuntimeRequested || r.parallelSwitchStatus != nil {
		a.parallelSwitchStatusPanel(c, r)
		return
	}
	ui.Text(c, T("environment.prepare_title")).Bold()
	if !switchAvailable(r) {
		muted(ui.Text(c, T("environment.prepare_no_switch"))).Wrap()
	} else {
		muted(ui.Text(c, T("environment.switch_ready_hint"))).Wrap()
	}
	if v := r.parallelStatus; v != nil {
		card(ui.Column(c)).Gap(8).Children(func() {
			fact(c, T("restore.target"), r.task.Target+" / "+r.task.Instance)
			fact(c, T("environment.prepare_mode"), T("environment.mode."+string(v.Mode)))
			if v.State == "historical" && v.Retired {
				muted(ui.Text(c, T("environment.historical_hint"))).Wrap()
				return
			}
			state := "environment.preparation_state.unknown"
			switch v.State {
			case "prepared":
				if r.task.Verified {
					state = "environment.preparation_state.prepared"
				}
			case "cancelled":
				state = "environment.preparation_state.cancelled"
			case "cleaned":
				state = "environment.preparation_state.cleaned"
			case "failed":
				state = "environment.preparation_state.failed"
			}
			ui.Text(c, T(state)).Wrap()
			original := "runtime.run_unknown"
			if v.OriginalObserved && v.OriginalRunning {
				original = "runtime.running"
			} else if v.OriginalObserved && v.OriginalStopped {
				original = "runtime.stopped"
			}
			fact(c, T("environment.prepare_original"), T(original))
			gateway, stopped := "restoretest.environment.no", "environment.prepare_probe_unknown"
			if v.Check != nil && v.Check.Verified() && v.Probe == "stopped" && v.IssueCode == "" && !v.Retired {
				gateway = "restoretest.environment.yes"
			}
			if v.Probe == "stopped" {
				stopped = "environment.prepare_probe_stopped"
			} else if v.Probe == "not_started" {
				stopped = "environment.prepare_probe_not_started"
			}
			fact(c, T("environment.prepare_gateway"), T(gateway))
			fact(c, T("environment.prepare_probe_state"), T(stopped))
			if v.IssueCode != "" || v.FailureCode != "" {
				ui.Text(c, T("environment.prepare_needs_review")).TextColor(palette.Warn).Wrap()
			}
			ui.Row(c).Gap(10).Children(func() {
				if switchAvailable(r) && v.State == "prepared" && v.Phase == environment.Prepared {
					ui.Button(c, T("environment.switch_review")).OnClick(func() { a.reviewParallelSwitch(r, "switch") })
				}
				if v.CanCancel {
					ui.Button(c, T("environment.prepare_cancel")).OnClick(func() { a.parallelPreparationAction(r, "cancel") })
				}
				if v.CanReviewCleanup {
					ui.Button(c, T("environment.prepare_review_cleanup")).OnClick(func() { a.parallelPreparationAction(r, "review_cleanup") })
				}
			})
			ui.Collapsible(c, T("environment.prepare_details"), a.fold("parallel-status-details"), func() {
				mono(muted(ui.Text(c, r.task.OpID))).Wrap()
				if v.FailureCode != "" {
					mono(muted(ui.Text(c, v.FailureCode))).Wrap()
				}
				if v.IssueCode != "" {
					mono(muted(ui.Text(c, v.IssueCode))).Wrap()
				}
			})
		})
	} else {
		muted(ui.Text(c, T("environment.prepare_unknown"))).Wrap()
	}
	if r.parallelCleanup != nil {
		a.parallelPreparationCleanupDialog(c, r)
	}
}

func (a *App) parallelPreparationCleanupDialog(c *ui.Context, r *restoreFlow) {
	review := r.parallelCleanup
	open := true
	_, height := c.Size()
	if ui.Modal(c, &open, func() {
		ui.Column(c).Width(500).Padding(24).Gap(12).Children(func() {
			ui.Text(c, T("environment.prepare_cleanup_title")).Bold().FontSize(18)
			muted(ui.Text(c, T("environment.prepare_cleanup_hint"))).Wrap()
			ui.Scroll(c).MaxHeight(max(160, height-220)).Children(func() {
				ui.Column(c).Gap(8).Children(func() {
					for _, item := range review.Items {
						if !item.Present {
							continue
						}
						key := "environment.material.other"
						switch item.Kind {
						case "program-original":
							key = "environment.material.original"
						case "program-candidate":
							key = "environment.material.candidate"
						case "stage":
							key = "environment.material.stage"
						case "workspace":
							key = "environment.material.workspace"
						case "reuse-data":
							key = "environment.material.reuse_data"
						case "reuse-program":
							key = "environment.material.reuse_program"
						case "reuse-workspace":
							key = "environment.material.reuse_workspace"
						}
						ui.Text(c, T(key)).Wrap()
					}
				})
			})
			ui.Row(c).Gap(10).Justify(ui.End).Children(func() {
				ui.Button(c, T("common.cancel")).OnClick(func() { r.parallelCleanup = nil })
				ui.Button(c, T("environment.prepare_cleanup")).OnClick(func() { a.parallelPreparationAction(r, "cleanup") })
			})
		})
	}).Dismissed() {
		r.parallelCleanup = nil
	}
}
