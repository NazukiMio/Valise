package gui

import (
	"context"
	"slices"
	"time"

	"valise/core/environment"
	"valise/ui/service"
)

func (a *App) readParallelSwitchCapability(r *restoreFlow) {
	if r.parallelSwitchCapability != nil || r.parallelCapabilityJob.busy || r.task.Target == "" {
		return
	}
	initial := false
	r.parallelSwitchCapability = &initial
	var supported bool
	a.start(&r.parallelCapabilityJob, time.Minute, func(ctx context.Context) error {
		caps, err := a.svc.BackupCapabilities(ctx, r.task.Target)
		supported = err == nil && slices.Contains(caps, environment.SwitchCapability)
		return err
	}, func(err error) {
		value := err == nil && supported
		r.parallelSwitchCapability = &value
	})
}

func switchAvailable(r *restoreFlow) bool {
	return r.parallelSwitchCapability != nil && *r.parallelSwitchCapability
}

func (a *App) reviewParallelSwitch(r *restoreFlow, action string) {
	if r.j.busy || !switchAvailable(r) {
		return
	}
	var reviewed *service.PreparedParallelSwitch
	task := r.task
	r.parallelSwitchConfirm, r.parallelSwitchDraft = false, false
	a.start(&r.j, 5*time.Minute, func(ctx context.Context) (err error) {
		reviewed, err = a.svc.PrepareParallelSwitch(a.restoreContext(ctx, r), task, action)
		return err
	}, func(err error) {
		if err == nil {
			r.parallelSwitchPlan, r.parallelSwitchAction, r.step = reviewed, action, "parallel_switch_review"
		}
		a.restoreSafeExit(r)
	})
}

func (a *App) runParallelSwitch(r *restoreFlow) {
	if r.j.busy || r.parallelSwitchPlan == nil || !r.parallelSwitchConfirm {
		return
	}
	plan, allow := r.parallelSwitchPlan, r.parallelSwitchDraft
	r.j = job{busy: true, doingAt: time.Now()}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	r.j.cancel = cancel
	ctx = a.restoreContext(ctx, r)
	a.run(func() {
		defer cancel()
		task, status, err := a.svc.RunParallelSwitch(ctx, plan, allow, func(task service.RestoreTask) {
			a.update(func() {
				r.task = task
				if task.Stage == service.StageSwap || task.Stage == service.StageEnvironmentConfirm {
					r.step = "swap"
				}
			})
		})
		a.update(func() {
			r.task, r.parallelSwitchStatus = task, status
			r.j.busy, r.j.err, r.j.cancel = false, err, nil
			r.parallelSwitchPlan, r.parallelSwitchConfirm = nil, false
			r.step, r.parallelRead = "parallel_preparation", true
			a.readRestoreTasks()
			a.refreshAgents()
			a.restoreSafeExit(r)
		})
	})
}

func (a *App) queryParallelSwitch(r *restoreFlow) {
	if r.j.busy {
		return
	}
	r.parallelRead = true
	task := r.task
	var view *environment.SwitchStatus
	a.start(&r.j, 3*time.Minute, func(ctx context.Context) (err error) {
		task, view, err = a.svc.InspectParallelSwitch(ctx, task)
		return err
	}, func(err error) {
		if err == nil {
			r.task, r.parallelSwitchStatus = task, view
		} else {
			r.parallelSwitchStatus = nil
		}
		a.restoreSafeExit(r)
	})
}
