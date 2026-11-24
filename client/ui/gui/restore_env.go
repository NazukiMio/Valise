package gui

import (
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/valise"
	"valise/ui/service"
)

func (r *restoreFlow) resetPlanOptIns() {
	r.draft, r.snapshotProfile, r.allowVersionMismatch, r.skipService = false, false, false, false
	r.envNeeded, r.envAutoSkipped = false, false
	r.inlineInstall = nil
}

func isBodyEnvBlocker(code string) bool {
	switch code {
	case "body_rebuild_unverified", "body_version_mismatch", "runtime_missing", "provenance_missing", "provenance_changed":
		return true
	}
	return false
}

func isServiceEnvBlocker(code string) bool {
	return code == "service_missing"
}

func isConsentBlocker(code string) bool {
	switch code {
	case "profile_draft", "profile_from_snapshot":
		return true
	}
	return false
}

func hasEnvBlocker(p *service.RevivePlan) bool {
	if p == nil {
		return false
	}
	if len(p.Nested) > 0 {
		return true
	}
	for _, b := range p.Blockers {
		if !isConsentBlocker(b.Code) {
			return true
		}
	}
	return false
}

func (a *App) restoreEnv(c *ui.Context, r *restoreFlow) {
	if r.j.busy {
		a.restoreProgress(c, r)
		return
	}
	if r.plan == nil {
		return
	}
	p := r.plan.Revive
	var hardBlocks []valise.UserError
	var bodyMissing, bodyMismatch, serviceMissing *valise.UserError
	for i := range p.Blockers {
		b := &p.Blockers[i]
		switch {
		case isConsentBlocker(b.Code):
			continue
		case b.Code == "body_version_mismatch":
			if bodyMismatch == nil {
				bodyMismatch = b
			}
		case isBodyEnvBlocker(b.Code):
			if bodyMissing == nil {
				bodyMissing = b
			}
		case isServiceEnvBlocker(b.Code):
			if serviceMissing == nil {
				serviceMissing = b
			}
		default:
			hardBlocks = append(hardBlocks, *b)
		}
	}

	subPanel(ui.Column(c)).Gap(6).Children(func() {
		ui.Text(c, T("restore.env.checklist")).Bold().FontSize(fsSub)
		if len(hardBlocks) > 0 || len(p.Nested) > 0 {
			statusRow(c, "danger", T("restore.env.step_checks"), T("restore.env.status_pending"))
		}
		if bodyMissing != nil || bodyMismatch != nil {
			statusRow(c, "warn", T("restore.env.step_body"), T("restore.env.status_pending"))
		} else {
			statusRow(c, "ok", T("restore.env.step_body"), T("restore.env.status_ready"))
		}
		switch {
		case serviceMissing != nil:
			statusRow(c, "warn", T("restore.env.step_service"), T("restore.env.status_pending"))
		case p.Plan.InstallService:
			statusRow(c, "ok", T("restore.env.step_service"), T("restore.env.status_auto_install"))
		default:
			statusRow(c, "ok", T("restore.env.step_service"), T("restore.env.status_ready"))
		}
	})

	switch {
	case len(hardBlocks) > 0 || len(p.Nested) > 0:
		a.restoreEnvHardCard(c, p, hardBlocks)
	case bodyMissing != nil:
		a.restoreEnvBodyMissingCard(c, r, p, bodyMissing)
	case bodyMismatch != nil:
		a.restoreEnvBodyMismatchCard(c, r, p, bodyMismatch)
	case serviceMissing != nil:
		a.restoreEnvServiceMissingCard(c, r, p, serviceMissing)
	}

	ui.Row(c).Gap(10).Justify(ui.End).Children(func() {
		ui.Button(c, T("restore.edit")).OnClick(func() {
			r.resetPlanOptIns()
			r.step = "setup"
		})
		ui.Button(c, T("restore.env.recheck")).Disabled(r.j.busy).OnClick(func() { a.prepareRestore(r) })
	})
}

func (a *App) restoreEnvHardCard(c *ui.Context, p *service.RevivePlan, blocks []valise.UserError) {
	card(ui.Column(c)).Gap(10).Children(func() {
		cardTitle(ui.Text(c, T("restore.env.hard_title")))
		for i := range blocks {
			b := blocks[i]
			ui.Box(c.Key("hard-" + b.Code)).Children(func() {
				errorBox(c, &b, a.fold("restore-env-hard-"+b.Code))
			})
		}
		if len(p.Nested) > 0 {
			notice(c, "warn", T("restore.nested", "names", strings.Join(p.Nested, ", ")))
		}
	})
}

func (a *App) restoreEnvBodyMissingCard(c *ui.Context, r *restoreFlow, p *service.RevivePlan, b *valise.UserError) {
	card(ui.Column(c)).Gap(10).Children(func() {
		cardTitle(ui.Text(c, T("restore.env.body_missing_title")))
		if b.Reason != "" {
			ui.Text(c, b.Reason).Wrap().Selectable()
		}
		if b.Suggestion != "" {
			muted(ui.Text(c, b.Suggestion)).FontSize(fsSub).Wrap().Selectable()
		}
		if b.Detail != "" || p.BodySummary != "" {
			ui.Collapsible(c, T("error.tech_info"), a.fold("restore-env-body-detail"), func() {
				if p.BodySummary != "" {
					mono(muted(ui.Text(c, p.BodySummary))).FontSize(fsCaption).Wrap().Selectable()
				}
				if b.Detail != "" {
					mono(muted(ui.Text(c, b.Detail))).FontSize(fsCaption).Wrap().Selectable()
				}
			})
		}
		a.restoreMissingBodyBox(c, r, p)
		subPanel(ui.Column(c)).Gap(8).Children(func() {
			ui.Checkbox(c, &r.allowVersionMismatch, T("restore.allow_body_data_only"))
			ui.Row(c).Justify(ui.End).Children(func() {
				ui.Button(c, T("restore.env.continue_data_only")).Disabled(!r.allowVersionMismatch || r.j.busy).OnClick(func() {
					a.prepareRestore(r)
				})
			})
		})
	})
}

func (a *App) restoreEnvBodyMismatchCard(c *ui.Context, r *restoreFlow, p *service.RevivePlan, b *valise.UserError) {
	card(ui.Column(c)).Gap(10).Children(func() {
		cardTitle(ui.Text(c, T("restore.env.body_mismatch_title")))
		if r.inlineInstall != nil && r.inlineInstall.host == p.Target && r.inlineInstall.run != nil && r.inlineInstall.run.res != nil && r.inlineInstall.run.res.Completed {
			notice(c, "ok", T("restore.env.after_install_mismatch"))
		}
		subPanel(ui.Column(c)).Gap(6).Children(func() {
			if p.BodySummary != "" {
				ui.Text(c, p.BodySummary).Wrap().Selectable()
			} else if b.Reason != "" {
				ui.Text(c, b.Reason).Wrap().Selectable()
			}
			if b.Suggestion != "" {
				muted(ui.Text(c, b.Suggestion)).FontSize(fsSub).Wrap().Selectable()
			}
		})
		ui.Checkbox(c, &r.allowVersionMismatch, T("restore.allow_version_mismatch"))
		ui.Row(c).Justify(ui.End).Children(func() {
			ui.PrimaryButton(c, T("restore.env.next")).Disabled(!r.allowVersionMismatch || r.j.busy).OnClick(func() {
				a.prepareRestore(r)
			})
		})
	})
}

func (a *App) restoreEnvServiceMissingCard(c *ui.Context, r *restoreFlow, p *service.RevivePlan, b *valise.UserError) {
	card(ui.Column(c)).Gap(10).Children(func() {
		cardTitle(ui.Text(c, T("restore.env.service_missing_title")))
		ui.Text(c, T("restore.env.service_missing_desc", "unit", orText(p.Plan.Unit, T("restore.none")))).Wrap().Selectable()
		if b.Suggestion != "" {
			muted(ui.Text(c, b.Suggestion)).FontSize(fsSub).Wrap().Selectable()
		}
		if p.ProfileOverrideStale {
			notice(c, "warn", T("agent.service.local_override_stale", "id", p.Plan.Profile.ID, "local", p.EffectiveProfileVersion, "official", p.OfficialProfileVersion))
			ui.Row(c).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
				ui.PrimaryButton(c, T("restore.env.restore_official_recheck")).Disabled(r.j.busy).OnClick(func() {
					a.restoreOfficialAndRecheck(r, p.Plan.Profile.ID)
				})
				ui.Button(c, T("agent.service.go_profiles")).OnClick(func() {
					a.setTab = 3
					a.navigate("/settings")
				})
			})
		}
		subPanel(ui.Column(c)).Gap(8).Children(func() {
			ui.Checkbox(c, &r.skipService, T("restore.allow_skip_service"))
			ui.Row(c).Justify(ui.End).Children(func() {
				ui.Button(c, T("restore.env.continue_skip_service")).Disabled(!r.skipService || r.j.busy).OnClick(func() {
					a.prepareRestore(r)
				})
			})
		})
	})
}

func (a *App) restoreOfficialAndRecheck(r *restoreFlow, profileID string) {
	if r.j.busy || profileID == "" {
		return
	}
	r.j = job{busy: true, doing: T("restore.reading"), doingAt: time.Now()}
	a.run(func() {
		err := a.svc.RemoveLocalProfile(profileID)
		a.update(func() {
			r.j.busy, r.j.err = false, err
			if err == nil {
				a.reloadProfiles()
				a.prepareRestore(r)
			}
		})
	})
}
