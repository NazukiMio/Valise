package gui

import (
	"context"
	"github.com/egoist/mygo/ui"
	"strings"
	"time"
	"valise/core/revive"
	"valise/ui/service"
)

func restoreStageText(stage string) string {
	switch stage {
	case "environment_switch_login":
		return T("environment.progress_switch_login")
	case "environment_capture":
		return T("environment.progress_capture")
	case "environment_build":
		return T("environment.progress_build")
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
		ui.Row(c).Gap(10).Children(func() {
			ui.Spinner(c)
			stage := restoreStageText(r.progress.Stage)
			if r.step == "swap" && r.task.Kind == service.RestoreKindEnvironment {
				stage = restoreStepLabel(r)
			}
			ui.Text(c, stage).Bold()
		})
		muted(ui.Text(c, T("restore.elapsed", "duration", backupDuration(c.Now().Sub(r.j.doingAt))))).Wrap()
		if p := r.progress; p.Total > 0 {
			ui.Progress(c, float64(p.Done)/float64(p.Total))
			muted(ui.Text(c, T("restore.bytes", "done", size(p.Done), "total", size(p.Total)))).Wrap()
		}
		if r.step == "swap" {
			hint := "restore.swap_in_progress"
			if r.parallelSwitchAction == "confirm" {
				hint = "environment.confirm_progress_hint"
			}
			muted(ui.Text(c, T(hint))).Wrap()
		} else {
			hint := "restore.preparing_hint"
			if r.parallelSwitchPlan != nil {
				hint = "environment.switch_login_hint"
			}
			muted(ui.Text(c, T(hint))).Wrap()
			ui.Button(c, T("restore.stop")).OnClick(func() { r.stopConfirm = true })
		}
	})
}

func (a *App) restoreConfirm(c *ui.Context, r *restoreFlow) {
	c.After(time.Second)
	card(ui.Column(c)).Gap(10).Children(func() {
		ui.Text(c, T("restore.second")).FontSize(18).Bold()
		if r.verify != nil {
			fact(c, T("restore.stage_verified"), T("restore.files_verified", "n", r.verify.Files, "size", size(r.verify.Bytes)))
		}
		fact(c, T("restore.target"), r.task.Target+" / "+r.task.Instance)
		muted(ui.Text(c, T("restore.second_hint"))).Wrap()
		if time.Now().After(r.deadline) {
			ui.Text(c, T("restore.expired")).TextColor(palette.Warn).Wrap()
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
		ui.Column(c).Width(460).Padding(24).Gap(12).Children(func() {
			ui.Text(c, T("restore.stop_title")).FontSize(18).Bold()
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
	r.j = job{busy: true, doingAt: time.Now()}
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
		ui.Column(c).Width(460).Padding(24).Gap(12).Children(func() {
			ui.Text(c, T("restore.recover_title")).FontSize(18).Bold()
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

func (a *App) restoreResult(c *ui.Context, r *restoreFlow) {
	t := r.task
	if r.j.busy {
		a.restoreProgress(c, r)
		return
	}
	card(ui.Column(c)).Gap(10).Children(func() {
		ui.Text(c, T("restore.state."+orText(t.State, "failed"))).FontSize(18).Bold()
		fact(c, T("restore.target"), t.Target+" / "+t.Instance)
		if t.PreBackupSnapshot != "" {
			fact(c, T("restore.backup_current_result"), T("restore.checked"))
		}
		verified := T("restore.unknown")
		if t.Verified {
			verified = T("restore.checked")
		}
		fact(c, T("restore.stage_verified"), verified)
		directory := T("restore.journal." + strings.ToLower(orText(t.SwapState, "unknown")))
		if t.Rollback != nil {
			directory = T("restore.rollback.directory." + t.Rollback.Directories)
		}
		fact(c, T("restore.directory"), directory)
		runtime, health := T("restore.unknown"), T("restore.unknown")
		if t.State == "succeeded" && t.SwapState == revive.StateHealthy {
			runtime, health = T("restore.service_checked"), T("restore.health_checked")
		}
		if t.SwapState == revive.StateRestored {
			runtime, health = T("restore.no_start"), T("restore.not_checked")
		}
		if t.Rollback != nil {
			runtime = T("restore.rollback.service." + t.Rollback.Service)
			health = T("restore.rollback.health." + t.Rollback.Health)
			fact(c, T("restore.body"), T("restore.rollback.program."+t.Rollback.Program))
			if t.Rollback.Problem != "" {
				ui.Text(c, T("restore.rollback.problem."+t.Rollback.Problem)).TextColor(palette.Warn).Wrap()
			}
		}
		fact(c, T("restore.service_result"), runtime)
		fact(c, T("restore.health_result"), health)
		relation := T("restore.no_relation")
		if t.Relation {
			relation = T("restore.relation_checked")
		}
		fact(c, T("restore.relation_result"), relation)
		ui.Collapsible(c, T("restore.kept"), a.fold("restore-kept"), func() {
			for _, path := range t.Prev {
				mono(muted(ui.Text(c, path))).Wrap()
			}
		})
		if t.SwapState == revive.StateRolledBack {
			muted(ui.Text(c, T("restore.rollback_hint"))).Wrap()
		}
		if t.State == "needs_recovery" || t.State == "reconciling" {
			muted(ui.Text(c, T("restore.reconcile_hint"))).Wrap()
		}
		ui.Row(c).Gap(8).Children(func() {
			if t.OpID != "" {
				ui.Button(c, T("restore.query")).OnClick(func() { a.queryRestore(r) })
				if t.State == "reconciling" || t.State == "needs_recovery" {
					ui.Button(c, T("restore.new_intent")).OnClick(func() { r.newIntent = true })
				}
			}
			if r.review != nil {
				for _, action := range r.review.Actions {
					ui.Button(c.Key(action), T("restore.recover."+action)).OnClick(func() { r.recoverAction = action })
				}
			}
		})
	})
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
			if t.State == "reconciling" || t.State == "needs_recovery" || t.Kind == service.RestoreKindEnvironment && t.State == "awaiting_human" || oldCleanupPending(t) {
				task := t
				ui.Row(c).Padding(8, 16).Gap(10).Children(func() {
					message := T("restore.unfinished", "target", task.Target)
					if task.State == "awaiting_human" {
						message = T("environment.awaiting_banner", "target", task.Target+" / "+task.Instance)
					} else if oldCleanupPending(task) {
						message = T("environment.old_cleanup_banner", "target", task.Target+" / "+task.Instance)
					}
					muted(ui.Text(c, message)).Grow(1).Wrap()
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
	if r.j.busy || r.task.State == "reconciling" || r.task.State == "needs_recovery" || r.task.Kind == service.RestoreKindEnvironment && r.task.State == "awaiting_human" || oldCleanupPending(r.task) {
		ui.Row(c).Padding(8, 16).Gap(10).Children(func() {
			message := T("restore.task_banner", "stage", restoreStepLabel(r))
			if !r.j.busy && r.task.State == "awaiting_human" {
				message = T("environment.awaiting_banner", "target", r.task.Target+" / "+r.task.Instance)
			} else if !r.j.busy && oldCleanupPending(r.task) {
				message = T("environment.old_cleanup_banner", "target", r.task.Target+" / "+r.task.Instance)
			}
			ui.Text(c, message).Grow(1).Wrap()
			ui.Button(c, T("restore.open_task")).OnClick(func() { a.navigate("/restore") })
		})
	}
}
