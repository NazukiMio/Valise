package gui

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"
	"valise/core/environment"
	"valise/ui/service"
)

func oldCleanupPending(t service.RestoreTask) bool {
	return t.Kind == service.RestoreKindEnvironment && (t.OldCleanupState == "pending" || t.OldCleanupState == "unverified")
}

func oldCleanupStateText(state string) string {
	switch state {
	case "retained":
		return T("environment.old_cleanup_retained")
	case "pending":
		return T("environment.old_cleanup_pending")
	case "cleaned":
		return T("environment.old_cleanup_cleaned")
	default:
		return T("environment.old_cleanup_unknown")
	}
}

func (a *App) reviewOldCleanup(r *restoreFlow) {
	if r.j.busy || !switchAvailable(r) {
		return
	}
	task := r.task
	var plan *service.PreparedOldCleanup
	r.oldCleanupConfirm = false
	a.start(&r.j, 4*time.Minute, func(ctx context.Context) (err error) {
		plan, err = a.svc.PrepareOldCleanup(a.restoreContext(ctx, r), task)
		return err
	}, func(err error) {
		if err == nil {
			r.oldCleanupPlan, r.step = plan, "parallel_old_cleanup"
		}
		a.restoreSafeExit(r)
	})
}

func (a *App) oldCleanupPage(c *ui.Context, r *restoreFlow) {
	if r.j.busy {
		a.jobLine(c, &r.j)
		return
	}
	p := r.oldCleanupPlan
	if p == nil {
		return
	}
	v := p.View
	c.After(time.Second)
	ui.Row(c).Gap(10).Children(func() {
		ui.Button(c, T("environment.switch_back")).OnClick(func() { r.step, r.oldCleanupPlan = "parallel_preparation", nil })
		ui.Button(c, T("common.refresh")).OnClick(func() { a.reviewOldCleanup(r) })
	})
	card(ui.Column(c)).Gap(10).Children(func() {
		ui.Text(c, T("environment.old_cleanup_title")).Bold()
		fact(c, T("restore.target"), r.task.Target+" / "+r.task.Instance)
		if v.PolicyMode == environment.Direct || v.PolicyMode == environment.Parallel || v.PolicyMode == environment.Compact {
			fact(c, T("environment.saved_mode"), T("environment.mode."+string(v.PolicyMode)))
		}
		fact(c, T("environment.old_cleanup_target"), T("environment.old_cleanup_slot", "slot", v.Target.Slot))
		fact(c, T("environment.old_cleanup_current"), T("environment.old_cleanup_slot", "slot", v.Current.Slot))
		muted(ui.Text(c, T("environment.old_cleanup_effect"))).Wrap()
		if v.State == "pending" {
			ui.Text(c, T("environment.old_cleanup_pending")).TextColor(palette.Warn).Wrap()
		}
		for _, item := range v.Items {
			label := T("environment.old_cleanup_temporary")
			switch item.Kind {
			case "data":
				label = T("environment.old_cleanup_data", "root", item.Root)
			case "program":
				label = T("environment.old_cleanup_program")
			}
			value := T("environment.old_cleanup_removed")
			if item.Present {
				value = T("restoretest.file_count", "n", item.Files, "size", size(item.Bytes))
			}
			fact(c, label, value)
		}
		muted(ui.Text(c, T("environment.old_cleanup_size_hint"))).Wrap()
		if v.State == "cleaned" {
			ui.Text(c, T("environment.old_cleanup_cleaned")).Wrap()
		} else if time.Now().After(p.Expires) {
			ui.Text(c, T("restore.expired")).TextColor(palette.Warn).Wrap()
		} else {
			ui.Checkbox(c, &r.oldCleanupConfirm, T("environment.old_cleanup_confirm"))
			label := "environment.old_cleanup_execute"
			if v.State == "pending" {
				label = "environment.old_cleanup_continue"
			}
			ui.Button(c, T(label)).Disabled(!r.oldCleanupConfirm).OnClick(func() { a.runOldCleanup(r) })
		}
		ui.Collapsible(c, T("environment.prepare_details"), a.fold("old-cleanup-details"), func() {
			mono(muted(ui.Text(c, v.OperationID))).Wrap()
			mono(muted(ui.Text(c, v.Digest))).Wrap()
		})
	})
}

func (a *App) runOldCleanup(r *restoreFlow) {
	if r.j.busy || !switchAvailable(r) || r.oldCleanupPlan == nil || !r.oldCleanupConfirm {
		return
	}
	p := r.oldCleanupPlan
	r.parallelSwitchAction = "cleanup"
	r.j = job{busy: true, doing: T("environment.old_cleanup_progress"), doingAt: time.Now()}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	r.j.cancel = cancel
	ctx = a.restoreContext(ctx, r)
	a.run(func() {
		defer cancel()
		task, status, err := a.svc.RunOldCleanup(ctx, p, func(task service.RestoreTask) {
			a.update(func() { r.task = task })
		})
		a.update(func() {
			r.task, r.parallelSwitchStatus = task, status
			r.j.busy, r.j.err, r.j.cancel = false, err, nil
			r.oldCleanupPlan, r.oldCleanupConfirm = nil, false
			r.step, r.parallelRead = "parallel_preparation", true
			a.readRestoreTasks()
			a.restoreSafeExit(r)
		})
	})
}

func (a *App) oldCleanupStatus(c *ui.Context, r *restoreFlow, v *environment.SwitchStatus) {
	if v.CleanupState != "" {
		ui.Text(c, oldCleanupStateText(v.CleanupState)).Wrap()
	}
	if switchAvailable(r) && v.CanReviewOldCleanup {
		ui.Button(c, T("environment.old_cleanup_review")).OnClick(func() { a.reviewOldCleanup(r) })
	}
}
