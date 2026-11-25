package gui

import (
	"context"
	"github.com/egoist/mygo/ui"
	"time"
	"valise/ui/service"
)

func restoreStageText(stage string) string {
	switch stage {
	case service.StageSealScan, service.StageSealPack, service.StageSealCommit:
		return T("restore.progress.backup_first")
	case service.StagePull, service.StageOpen, service.StageInspect, service.StageStage, service.StageVerify, service.StageSwap:
		return T("restore.progress." + stage)
	}
	return T("restore.reading")
}
func (a *App) restoreProgress(c *ui.Context, r *restoreFlow) {
	c.After(time.Second)
	card(ui.Column(c)).Gap(12).Children(func() {
		stageText := restoreStageText(r.progress.Stage)
		if r.progress.Stage == "" && r.j.doing != "" {
			stageText = r.j.doing
		}
		ui.Row(c).Gap(10).Children(func() {
			ui.Spinner(c)
			ui.Text(c, stageText).Bold()
		})
		muted(ui.Text(c, T("restore.elapsed", "duration", backupDuration(c.Now().Sub(r.j.doingAt))))).Wrap()
		if p := r.progress; p.Total > 0 {
			ui.Progress(c, float64(p.Done)/float64(p.Total))
			muted(ui.Text(c, T("restore.bytes", "done", size(p.Done), "total", size(p.Total)))).Wrap()
		}
		if r.step == "swap" {
			muted(ui.Text(c, T("restore.swap_in_progress"))).Wrap()
		} else {
			muted(ui.Text(c, T("restore.preparing_hint"))).Wrap()
			ui.Button(c, T("restore.stop")).OnClick(func() { r.stopConfirm = true })
		}
	})
}

func (a *App) restoreConfirm(c *ui.Context, r *restoreFlow) {
	c.After(time.Second)
	card(ui.Column(c)).Gap(10).Children(func() {
		dialogTitle(ui.Text(c, T("restore.second")))
		if r.verify != nil {
			fact(c, T("restore.stage_verified"), T("restore.files_verified", "n", r.verify.Files, "size", size(r.verify.Bytes)))
		}
		fact(c, T("restore.target"), r.task.Target+" / "+r.task.Instance)
		muted(ui.Text(c, T("restore.second_hint"))).Wrap()
		if time.Now().After(r.deadline) {
			notice(c, "warn", T("restore.expired"))
			ui.Button(c, T("restore.recheck_stage")).OnClick(func() { a.recheckRestore(r) })
		} else {
			ui.Checkbox(c, &r.swap, T("restore.swap_confirm"))
			ui.PrimaryButton(c, T("restore.execute."+r.mode)).Disabled(!r.swap).OnClick(func() {
				// The worker also verifies expiry and all target facts after this reply.
				if r.reply != nil && time.Now().Before(r.deadline) {
					r.reply <- restoreSwapReply{approve: true, deadline: r.deadline}
					r.step = "prepare"
					r.swap = false
				}
			})
		}
		ui.Button(c, T("restore.hold")).OnClick(func() { r.stopConfirm = true })
	})
}

func (a *App) recheckRestore(r *restoreFlow) {
	if r.task.OpID == "" {
		return
	}
	r.swap = false
	plan := r.plan
	if plan == nil {
		return
	}
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		err := a.svc.RecheckRestoreStage(ctx, plan)
		a.update(func() {
			r.j.err = err
			if err == nil {
				r.deadline = time.Now().Add(10 * time.Minute)
			}
		})
	})
}

func (a *App) restoreStopDialog(c *ui.Context, r *restoreFlow) {
	open := true
	if ui.Modal(c, &open, func() {
		dialog(ui.Column(c), 460).Children(func() {
			dialogTitle(ui.Text(c, T("restore.stop_title")))
			muted(ui.Text(c, T("restore.stop_hint"))).Wrap()
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				ui.Button(c, T("common.cancel")).OnClick(func() { r.stopConfirm = false })
				ui.Button(c, T("restore.stop_confirm")).OnClick(func() { a.stopRestore(r) })
			})
		})
	}).Dismissed() {
		r.stopConfirm = false
	}
}

func (a *App) queryRestore(r *restoreFlow) {
	if r.j.busy {
		return
	}
	r.progress = service.Progress{}
	r.j = job{busy: true, doing: T("restore.querying"), doingAt: time.Now()}
	task := r.task
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		t, review, err := a.svc.InspectRestore(ctx, task)
		a.update(func() {
			r.j.busy, r.j.err = false, err
			if err == nil {
				r.task, r.review = t, review
				r.step = "result"
				if t.State == "needs_recovery" {
					r.step = "reconcile"
				}
			}
			a.restoreSafeExit(r)
		})
	})
}

func (a *App) restoreRecoveryDialog(c *ui.Context, r *restoreFlow) {
	open := true
	if ui.Modal(c, &open, func() {
		dialog(ui.Column(c), 460).Children(func() {
			dialogTitle(ui.Text(c, T("restore.recover_title")))
			muted(ui.Text(c, T("restore.recover_hint"))).Wrap()
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				ui.Button(c, T("common.cancel")).OnClick(func() { r.recoverAction = "" })
				ui.PrimaryButton(c, T("restore.recover."+r.recoverAction)).OnClick(func() {
					task, review, action := r.task, r.review, r.recoverAction
					r.recoverAction = ""
					r.j = job{busy: true, doingAt: time.Now()}
					r.step = "swap"
					a.run(func() {
						t, err := a.svc.ResumeRestore(context.Background(), task, review, action)
						a.update(func() {
							r.task = t
							r.j.busy, r.j.err = false, err
							r.review = nil
							r.step = "reconcile"
							if err == nil {
								r.step = "result"
							}
							a.restoreSafeExit(r)
						})
					})
				})
			})
		})
	}).Dismissed() {
		r.recoverAction = ""
	}
}

// restoreBanner is outside the theme's layout slots: active work and unknown
// results remain reachable after navigating away, including after a restart.
func (a *App) restoreBanner(c *ui.Context) {
	r := a.restore
	if a.restoreTasksErr != nil {
		errorBox(c, a.restoreTasksErr, a.fold("restore-index-error"))
	}
	if r == nil {
		for _, t := range a.restoreTasks {
			if t.State == "reconciling" || t.State == "needs_recovery" {
				task := t
				banner(c, "warn", bannerText(c, T("restore.unfinished", "target", task.Target), ""), func() {
					ui.Button(c, T("restore.open_task")).OnClick(func() {
						a.restore = reopenRestoreTask(task)
						a.navigate("/restore")
					})
				})
				return
			}
		}
		return
	}
	if r.j.busy || r.task.State == "reconciling" || r.task.State == "needs_recovery" {
		tone := "busy"
		if !r.j.busy {
			tone = "warn"
		}
		banner(c, tone, bannerText(c, T("restore.task_banner", "stage", T("restore.step."+r.step)), ""), func() {
			ui.Button(c, T("restore.open_task")).OnClick(func() { a.navigate("/restore") })
		})
	}
}
