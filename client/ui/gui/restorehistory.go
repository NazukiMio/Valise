package gui

import (
	"github.com/egoist/mygo/ui"
	"valise/core/journey"
	"valise/ui/service"
)

func (a *App) readRestoreTasks() {
	a.run(func() {
		tasks, err := a.svc.ListRestoreTasks()
		a.update(func() { a.restoreTasks, a.restoreTasksErr = tasks, err })
	})
}

func (a *App) restoreTaskList(c *ui.Context) {
	ui.Link(c, "‹ "+T("nav.settings"), "/settings")
	pageHead(c, T("restore.history"), T("restore.history_hint"))
	if a.restoreTasksErr != nil {
		errorBox(c, a.restoreTasksErr, a.fold("restore-history-error"))
	}
	if len(a.restoreTasks) == 0 {
		muted(ui.Text(c, T("restore.history_empty"))).Wrap()
	}
	for _, task := range a.restoreTasks {
		card(ui.Row(c.Key(task.OpID))).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).Gap(3).Children(func() {
				ui.Text(c, task.Target+" / "+task.Instance).Bold()
				muted(ui.Text(c, T("restore.mode."+task.Mode)+" · "+T("restore.state."+task.State))).Wrap()
				if task.OldCleanupState != "" {
					muted(ui.Text(c, oldCleanupStateText(task.OldCleanupState))).Wrap()
				}
				muted(ui.Text(c, T("restore.observed", "time", task.Updated.Local().Format("2006-01-02 15:04:05")))).Wrap()
			})
			ui.Button(c, T("restore.open_task")).OnClick(func() {
				if a.restore == nil || !a.restore.j.busy {
					a.restore = reopenRestoreTask(task)
				}
				a.navigate("/restore")
			})
		})
	}
	ui.Button(c, T("restore.refresh_history")).OnClick(a.readRestoreTasks)
}

func reopenRestoreTask(task service.RestoreTask) *restoreFlow {
	step := "result"
	if task.State == "reconciling" || task.State == "needs_recovery" {
		step = "reconcile"
	}
	r := &restoreFlow{task: task, mode: task.Mode, origin: task.Target, step: step}
	if task.Kind == service.RestoreKindEnvironment {
		r.step = "parallel_preparation"
		r.point = journey.TimelinePoint{SnapshotRef: task.Snapshot, CreatedAt: task.SnapshotTime, Scope: journey.Scope{Tier: task.SnapshotTier}}
	}
	return r
}

func (a *App) restoreNewDialog(c *ui.Context, r *restoreFlow) {
	open := true
	if ui.Modal(c, &open, func() {
		ui.Column(c).Width(480).Padding(24).Gap(12).Children(func() {
			ui.Text(c, T("restore.new_title")).Bold().FontSize(18)
			muted(ui.Text(c, T("restore.new_hint"))).Wrap()
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				ui.Button(c, T("common.cancel")).OnClick(func() { r.newIntent = false })
				ui.Button(c, T("restore.new_go")).OnClick(func() { a.restore = nil; a.readRestoreTasks(); a.navigate("/agents") })
			})
		})
	}).Dismissed() {
		r.newIntent = false
	}
}
