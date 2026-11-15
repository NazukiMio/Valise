package gui

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"
	"valise/core/environment"
	"valise/ui/service"
)

func (a *App) parallelPrepareChoices(c *ui.Context, r *restoreFlow) {
	p := r.parallelPrepared
	if p == nil {
		return
	}
	c.After(time.Second)
	card(ui.Column(c)).Gap(10).Children(func() {
		ui.Text(c, T("environment.prepare_title")).Bold()
		muted(ui.Text(c, T("environment.prepare_effect"))).Wrap()
		if m := p.Materials; m != nil {
			if m.BaselineLocation != nil {
				fact(c, T("restoretest.materials.baseline_location"), journeyLocation(*m.BaselineLocation))
			}
			muted(ui.Text(c, T("environment.prepare_baseline_hint"))).Wrap()
			if m.Partial {
				ui.Text(c, T("restoretest.materials.partial")).TextColor(palette.Warn).Wrap()
			}
			ui.Checkbox(c, &r.parallelBaseline, T("environment.prepare_baseline_confirm"))
		}
		if p.RequiresCredentials {
			muted(ui.Text(c, T("restore.secret_restore_hint"))).Wrap()
			ui.Checkbox(c, &r.credential, T("restore.secret_confirm"))
		}
		if p.View.ProfileUntested {
			muted(ui.Text(c, T("environment.prepare_draft_hint"))).Wrap()
			ui.Checkbox(c, &r.draft, T("restore.allow_draft"))
		}
		if time.Now().After(p.Expires) {
			ui.Text(c, T("restore.expired")).TextColor(palette.Warn).Wrap()
			return
		}
		allowed := (!p.RequiresCredentials || r.credential) && (!p.View.ProfileUntested || r.draft) && (p.Materials == nil || r.parallelBaseline)
		label := "environment.prepare"
		if p.View.Reuse != nil {
			label = "environment.reuse_prepare"
		}
		ui.PrimaryButton(c, T(label)).Disabled(!allowed).OnClick(func() { a.needKey(func() { a.runParallelPreparation(r) }) })
	})
}

func (a *App) runParallelPreparation(r *restoreFlow) {
	if r.j.busy || r.parallelPrepared == nil {
		return
	}
	p, choice := r.parallelPrepared, service.ParallelPrepareChoice{Credentials: r.credential, AllowUntested: r.draft, Baseline: r.parallelBaseline, Reuse: r.parallelPrepared.View.Reuse != nil}
	r.j, r.step = job{busy: true, doingAt: time.Now()}, "parallel_preparation"
	r.parallelRead, r.parallelStatus = true, nil
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	r.j.cancel = cancel
	ctx = a.restoreContext(ctx, r)
	a.run(func() {
		defer cancel()
		task, err := a.svc.RunParallelPreparation(ctx, p, choice, func(task service.RestoreTask) {
			a.update(func() { r.task = task })
		})
		var statusErr error
		var observed *environment.PreparationStatus
		if task.OpID != "" {
			readCtx, done := context.WithTimeout(context.Background(), time.Minute)
			task, observed, statusErr = a.svc.InspectParallelPreparation(readCtx, task)
			done()
		}
		if err == nil {
			err = statusErr
		}
		a.update(func() {
			r.task, r.parallelStatus = task, observed
			r.j.busy, r.j.err, r.j.cancel = false, err, nil
			r.parallelPrepared = nil
			a.readRestoreTasks()
			a.refreshAgents()
			a.restoreSafeExit(r)
		})
	})
}

func (a *App) parallelPreparationAction(r *restoreFlow, action string) {
	if r.j.busy || r.task.Kind != service.RestoreKindEnvironment {
		return
	}
	if action == "status" {
		r.parallelSwitchCapability = nil
		if r.task.RuntimeRequested {
			a.queryParallelSwitch(r)
			return
		}
	}
	r.parallelRead = true
	task, cleanup := r.task, r.parallelCleanup
	if action == "cleanup" {
		r.parallelCleanup = nil
	}
	var view = r.parallelStatus
	var review = r.parallelCleanup
	a.start(&r.j, 3*time.Minute, func(ctx context.Context) (err error) {
		ctx = a.restoreContext(ctx, r)
		switch action {
		case "status":
			task, view, err = a.svc.InspectParallelPreparation(ctx, task)
		case "cancel":
			task, view, err = a.svc.CancelParallelPreparation(ctx, task)
		case "review_cleanup":
			review, err = a.svc.ReviewParallelPreparationCleanup(ctx, task)
		case "cleanup":
			if cleanup != nil {
				task, view, err = a.svc.CleanupParallelPreparation(ctx, task, *cleanup)
			}
		}
		return err
	}, func(err error) {
		if err == nil {
			r.task, r.parallelStatus = task, view
			if action == "cancel" || action == "cleanup" {
				r.parallelSwitchStatus = nil
			}
			if action == "review_cleanup" {
				r.parallelCleanup = review
			} else {
				r.parallelCleanup = nil
			}
		} else if action != "review_cleanup" {
			r.parallelStatus = nil
		}
		a.readRestoreTasks()
		a.restoreSafeExit(r)
	})
}
