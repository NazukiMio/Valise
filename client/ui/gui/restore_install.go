package gui

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"valise/client/config"
	"valise/core/valise"
	"valise/ui/service"
)

// Inline custom install inside the restore plan when the target server has no
// matching harness body and no automated rebuild steps (body_rebuild_unverified).

type restoreInstallState struct {
	host      string
	profileID string
	run       *customInstallRun
}

func (a *App) ensureRestoreInlineInstall(r *restoreFlow, host, profileID string) *customInstallRun {
	if r.inlineInstall == nil || r.inlineInstall.host != host || r.inlineInstall.profileID != profileID {
		ci := &customInstallRun{}
		ci.onDone = func(res *service.InstallResult, err error) {
			if err == nil && res != nil && res.Completed {
				a.prepareRestore(r)
			}
		}
		r.inlineInstall = &restoreInstallState{host: host, profileID: profileID, run: ci}
	}
	return r.inlineInstall.run
}

func (a *App) restoreMissingBodyBox(c *ui.Context, r *restoreFlow, p *service.RevivePlan) {
	host := p.Target
	profileID := p.Plan.Profile.ID
	ci := a.ensureRestoreInlineInstall(r, host, profileID)

	subPanel(ui.Column(c)).Gap(8).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
			ui.Text(c, T("restore.inline_install.title")).Bold().Grow(1)
			ui.Button(c, T("restore.go_install_target")).OnClick(func() { a.navigate("/servers/" + host + "/agents") })
		})
		muted(ui.Text(c, T("restore.inline_install.hint"))).FontSize(fsSub).Wrap()
		a.loadSavedInstalls()
		presets := filterSavedInstalls(a.savedInstalls, profileID)
		if len(presets) > 0 && ci.conn == nil && ci.plan == nil {
			ui.Column(c).Gap(6).Children(func() {
				muted(ui.Text(c, T("restore.inline_install.presets"))).FontSize(fsCaption)
				ui.Row(c).Gap(8).Wrap().Children(func() {
					for _, item := range presets {
						it := item
						ui.Button(c.Key("ri-pre-"+it.ID), it.Name).OnClick(func() {
							ci.raw = it.Raw
							a.prepareCustomInstallForProfile(host, ci, profileID)
						})
					}
				})
			})
		}

		switch {
		case ci.conn != nil:
			a.renderTerminalPane(c, ci.step, ci.running, ci.term, &ci.text, &ci.input, ci.conn)
			if !ci.running {
				a.restoreInlineInstallOutcome(c, r, host, profileID, ci)
			}
		case ci.plan != nil:
			a.customPlanBody(c, host, ci, false, func() { ci.plan, ci.riskErr, ci.copied = nil, nil, "" })
		default:
			ui.TextArea(c, &ci.raw).Height(64).Label(T("install.custom.input_label")).Placeholder(T("install.custom.input_placeholder"))
			if ci.planning {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Spinner(c)
					ui.Text(c, T("install.custom.loading"))
				})
				return
			}
			if ci.planErr != nil {
				errorBox(c, ci.planErr, a.fold("ri-plan-err-"+host+"-"+profileID))
			}
			ui.Row(c).Gap(8).Justify(ui.End).Wrap().Children(func() {
				ui.PrimaryButton(c, T("restore.inline_install.prepare")).OnClick(func() {
					a.prepareCustomInstallForProfile(host, ci, profileID)
				})
			})
		}
	})
}

func (a *App) restoreInlineInstallOutcome(c *ui.Context, r *restoreFlow, host, profileID string, ci *customInstallRun) {
	res := ci.res
	gated := false
	if e, ok := valise.As(ci.err); ok && e.Code == service.GateCode {
		gated = true
	}
	if res != nil && len(res.Exits) > 0 {
		muted(ui.Text(c, exitLine(res.Exits))).Wrap().Selectable()
	}
	if ci.err != nil {
		if gated {
			ui.Text(c, T("install.custom.backup_needed")).Wrap()
			ui.Checkbox(c, &ci.acceptRisk, T("install.custom.accept_risk"))
		} else {
			errorBox(c, ci.err, a.fold("ri-run-err-"+host+"-"+profileID))
		}
	} else if res != nil && res.Completed {
		notice(c, "ok", T("restore.inline_install.done"))
	}
	ui.Row(c).Gap(8).Justify(ui.End).Wrap().Children(func() {
		ui.Button(c, T("install.custom.edit")).OnClick(func() { ci.conn, ci.err, ci.res = nil, nil, nil })
		if gated && ci.acceptRisk {
			ui.PrimaryButton(c, T("install.custom.continue_once")).OnClick(func() { a.startCustomInstall(host, ci, true) })
		}
		if res != nil && res.Completed {
			ui.PrimaryButton(c, T("restore.inline_install.replan")).OnClick(func() { a.prepareRestore(r) })
		}
	})
}

func filterSavedInstalls(all []config.SavedInstall, profileID string) []config.SavedInstall {
	if len(all) == 0 {
		return nil
	}
	var matched []config.SavedInstall
	for _, it := range all {
		if profileID != "" && strings.EqualFold(it.ProfileID, profileID) {
			matched = append(matched, it)
		}
	}
	if len(matched) > 0 {
		return matched
	}
	return all
}
