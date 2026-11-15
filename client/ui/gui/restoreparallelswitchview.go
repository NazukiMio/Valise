package gui

import (
	"time"

	"github.com/egoist/mygo/ui"
	"valise/core/environment"
	"valise/ui/service"
)

func restoreStepLabel(r *restoreFlow) string {
	if r.step == "parallel_switch_review" && r.parallelSwitchAction == "confirm" {
		return T("environment.confirm_title")
	}
	if r.step == "parallel_switch_review" && r.parallelSwitchAction == "return" {
		return T("environment.return_review_title")
	}
	if r.step == "parallel_preparation" && r.task.RuntimeRequested {
		return T("environment.runtime_status_title")
	}
	if r.step == "swap" && r.task.Kind == service.RestoreKindEnvironment {
		if r.parallelSwitchAction == "confirm" {
			return T("environment.confirm_progress")
		}
		if r.parallelSwitchAction == "return" {
			return T("environment.return_progress")
		}
		return T("environment.switch_progress")
	}
	return T("restore.step." + r.step)
}

func (a *App) parallelSwitchReview(c *ui.Context, r *restoreFlow) {
	if r.j.busy {
		a.restoreProgress(c, r)
		return
	}
	p := r.parallelSwitchPlan
	if p == nil {
		return
	}
	v, returning, confirming := p.View, r.parallelSwitchAction == "return", r.parallelSwitchAction == "confirm"
	c.After(time.Second)
	ui.Row(c).Gap(10).Children(func() {
		ui.Button(c, T("environment.switch_back")).OnClick(func() { r.step = "parallel_preparation"; r.parallelSwitchPlan = nil })
		ui.Button(c, T("common.refresh")).OnClick(func() { a.reviewParallelSwitch(r, r.parallelSwitchAction) })
	})
	card(ui.Column(c)).Gap(10).Children(func() {
		title := "environment.switch_review_title"
		if returning {
			title = "environment.return_review_title"
		} else if confirming {
			title = "environment.confirm_title"
		}
		ui.Text(c, T(title)).Bold()
		fact(c, T("restore.target"), r.task.Target+" / "+r.task.Instance)
		fact(c, T("environment.prepare_mode"), T("environment.mode."+string(v.Status.Mode)))
		check := func(label string, passed bool) {
			value := "restoretest.environment.no"
			if passed {
				value = "restoretest.environment.yes"
			}
			fact(c, T(label), T(value))
		}
		if !confirming {
			check("environment.switch_original_program", v.OriginalProgramChecked)
		}
		if !returning {
			check("environment.switch_candidate_program", v.CandidateProgramChecked)
			if !confirming {
				check("environment.switch_stage", v.StageChecked)
				check("environment.switch_source", v.SourceChecked)
				fact(c, T("restore.data"), T("restoretest.file_count", "n", v.Files, "size", size(v.Bytes)))
			}
		}
		if returning {
			muted(ui.Text(c, T("environment.return_effect"))).Wrap()
		} else if confirming {
			check("runtime.title", v.Status.CurrentObserved && v.Status.CurrentRunning && v.Status.Current != nil && *v.Status.Current == v.Status.Candidate)
			muted(ui.Text(c, T("environment.confirm_effect"))).Wrap()
			if !v.OriginalProgramChecked {
				ui.Text(c, T("environment.confirm_fallback_unverified")).TextColor(palette.Warn).Wrap()
			}
		} else {
			muted(ui.Text(c, T("environment.switch_effect"))).Wrap()
			boot := "environment.preview_boot_off"
			if v.EnableOnBoot {
				boot = "environment.preview_boot_on"
			}
			fact(c, T("environment.preview_boot"), T(boot))
		}
		allowed := returning && v.CanReturn || confirming && v.CanConfirm || !returning && !confirming && v.CanSwitch
		if !allowed {
			key := "environment.switch_not_ready"
			if returning {
				key = "environment.return_not_ready"
			} else if confirming {
				key = "environment.confirm_not_ready"
			}
			ui.Text(c, T(key)).TextColor(palette.Warn).Wrap()
		} else if time.Now().After(p.Expires) {
			ui.Text(c, T("restore.expired")).TextColor(palette.Warn).Wrap()
		} else {
			needsDraft := !returning && !confirming && v.ProfileUntested
			if needsDraft {
				ui.Text(c, T("environment.prepare_draft_hint")).Wrap()
				ui.Checkbox(c, &r.parallelSwitchDraft, T("restore.allow_draft"))
			}
			confirm, execute := "environment.switch_confirm", "environment.switch_execute"
			if returning {
				confirm, execute = "environment.return_confirm", "environment.return_execute"
			} else if confirming {
				confirm, execute = "environment.confirm_dialogue", "environment.confirm_execute"
			}
			ui.Checkbox(c, &r.parallelSwitchConfirm, T(confirm))
			ui.PrimaryButton(c, T(execute)).Disabled(!r.parallelSwitchConfirm || needsDraft && !r.parallelSwitchDraft).OnClick(func() { a.runParallelSwitch(r) })
		}
		ui.Collapsible(c, T("environment.prepare_details"), a.fold("parallel-switch-details"), func() {
			mono(muted(ui.Text(c, v.Status.OperationID))).Wrap()
			digest := v.Digest
			if confirming {
				digest = v.ConfirmationDigest
			}
			mono(muted(ui.Text(c, digest))).Wrap()
			if v.Status.IssueCode != "" {
				mono(muted(ui.Text(c, v.Status.IssueCode))).Wrap()
			}
		})
	})
}

func (a *App) parallelSwitchStatusPanel(c *ui.Context, r *restoreFlow) {
	v := r.parallelSwitchStatus
	if v == nil {
		message := "environment.switch_unknown"
		if r.parallelSwitchAction == "cleanup" || oldCleanupPending(r.task) {
			message = "environment.old_cleanup_unknown"
		}
		muted(ui.Text(c, T(message))).Wrap()
		return
	}
	card(ui.Column(c)).Gap(10).Children(func() {
		state := "environment.switch_unknown"
		switch v.Phase {
		case environment.Prepared:
			state = "environment.preparation_state.prepared"
		case environment.AwaitingHuman:
			state = "environment.switch_awaiting_human"
		case environment.Accepted, environment.Cleaning:
			state = "environment.switch_accepted"
		case environment.RolledBack:
			state = "environment.switch_returned"
		}
		ui.Text(c, T(state)).Bold().Wrap()
		if v.Historical {
			muted(ui.Text(c, T("environment.historical_hint"))).Wrap()
			if v.HistoricalConfirmed {
				ui.Text(c, T("environment.historical_confirmed")).Wrap()
			}
			return
		}
		if v.Current != nil && v.CurrentObserved {
			fact(c, T("runtime.title"), T("runtime.slot", "slot", v.Current.Slot))
		}
		running := "runtime.run_unknown"
		if v.CurrentObserved && v.CurrentRunning {
			running = "runtime.running"
		} else if v.CurrentObserved && v.CurrentStopped {
			running = "runtime.stopped"
		}
		ui.Text(c, T(running)).Wrap()
		gateway := "runtime.gateway_pending"
		if v.GatewayObserved && v.PortOwned && v.HTTPResponded && v.GatewayPassed {
			gateway = "runtime.gateway_passed"
		}
		ui.Text(c, T(gateway)).Wrap()
		conversation := "runtime.conversation_pending"
		if v.ConversationKnown {
			conversation = "runtime.conversation_confirmed"
		}
		ui.Text(c, T(conversation)).Wrap()
		a.oldCleanupStatus(c, r, v)
		if switchAvailable(r) {
			if v.Phase == environment.AwaitingHuman {
				ui.Button(c, T("environment.confirm_agent")).OnClick(func() { a.openAgent(r.task.Target, r.task.Instance) })
				ui.Button(c, T("environment.confirm_review")).OnClick(func() { a.reviewParallelSwitch(r, "confirm") })
			}
			if v.Phase == environment.Prepared {
				ui.Button(c, T("environment.switch_review")).OnClick(func() { a.reviewParallelSwitch(r, "switch") })
			} else if v.Phase != environment.RolledBack && v.Phase != environment.Cancelled && v.Phase != environment.Cleaning && !v.OriginalCleaned {
				ui.Button(c, T("environment.return_review")).OnClick(func() { a.reviewParallelSwitch(r, "return") })
			}
		}
		if v.FailureCode != "" || v.IssueCode != "" {
			ui.Text(c, T("environment.switch_needs_review")).TextColor(palette.Warn).Wrap()
		}
		ui.Collapsible(c, T("environment.prepare_details"), a.fold("parallel-switch-result"), func() {
			mono(muted(ui.Text(c, v.OperationID))).Wrap()
			if v.FailureCode != "" {
				mono(muted(ui.Text(c, v.FailureCode))).Wrap()
			}
			if v.IssueCode != "" {
				mono(muted(ui.Text(c, v.IssueCode))).Wrap()
			}
			if v.CleanupIssueCode != "" {
				mono(muted(ui.Text(c, v.CleanupIssueCode))).Wrap()
			}
		})
	})
}
