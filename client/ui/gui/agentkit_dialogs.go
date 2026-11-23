package gui

import (
	"context"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/agentkit"
	"valise/ui/service"
)

func (a *App) kitInstallModal(c *ui.Context, row *agentRow, st *agentKitState) {
	if !st.installOpen || st.status == nil {
		return
	}
	open := true
	if ui.Modal(c, &open, func() {
		dialog(ui.Column(c), 520).Children(func() {
			dialogTitle(ui.Text(c, T("kit.install_title")))
			ui.Text(c, T("kit.install_what", "dir", st.status.Dir)).Wrap()
			hasMod := false
			ui.Scroll(c).MaxHeight(200).Children(func() {
				subPanel(ui.Column(c)).Gap(6).Children(func() {
					for _, s := range st.status.Skills {
						tag := T("kit.preview.write")
						switch s.State {
						case agentkit.SkillModified:
							tag = T("kit.preview.overwrite_backup")
							hasMod = true
						case agentkit.SkillCurrent:
							tag = T("kit.preview.same")
						}
						ui.Row(c.Key("pki-" + s.Skill)).Gap(8).Children(func() {
							mono(muted(ui.Text(c, s.Path))).FontSize(fsSub).Grow(1).Wrap()
							muted(ui.Text(c, tag)).FontSize(fsSub)
						})
					}
				})
			})
			if hasMod {
				notice(c, "warn", T("kit.install_backup_warn"))
			}
			if st.guide != nil && len(st.guide.MCPCommand) > 0 {
				ui.Checkbox(c, &st.wireMCP, T("kit.wire_mcp_ask"))
				codeBox(mono(ui.Text(c, strings.Join(st.guide.MCPCommand, " "))).FontSize(fsCaption).Wrap())
			}
			if st.guide != nil && st.guide.HasService {
				ui.Checkbox(c, &st.restartSvc, T("kit.restart_svc_ask", "unit", st.guide.ServiceUnit))
			}
			if st.actJ.busy {
				a.jobLine(c, &st.actJ)
				return
			}
			if st.actJ.err != nil {
				errorBox(c, st.actJ.err, a.fold("kit-inst-err-"+row.Server+"/"+row.Instance))
			}
			need := T("need.valise")
			if st.wireMCP {
				need = T("need.admin")
			}
			ui.Row(c).Gap(8).AlignItems(ui.Center).Justify(ui.End).Children(func() {
				needTag(c, need)
				ui.Button(c, T("common.cancel")).OnClick(func() {
					st.installOpen = false
					st.actJ.err = nil
				})
				ui.PrimaryButton(c, T("kit.install_confirm")).OnClick(func() {
					a.runKitInstall(row.Server, row.Instance, st)
				})
			})
		})
	}).Dismissed() && !st.actJ.busy {
		st.installOpen = false
		st.actJ.err = nil
	}
}

func (a *App) kitUninstallModal(c *ui.Context, row *agentRow, st *agentKitState) {
	if !st.uninstallOpen || st.status == nil {
		return
	}
	open := true
	if ui.Modal(c, &open, func() {
		dialog(ui.Column(c), 520).Children(func() {
			dialogTitle(ui.Text(c, T("kit.uninstall_title")))
			ui.Text(c, T("kit.uninstall_what")).Wrap()
			ui.Scroll(c).MaxHeight(180).Children(func() {
				subPanel(ui.Column(c)).Gap(4).Children(func() {
					for _, s := range st.status.Skills {
						if s.State == agentkit.SkillMissing {
							continue
						}
						line := T("kit.uninstall_remove", "path", s.Path)
						if s.State == agentkit.SkillModified && !st.uninstallForce {
							line = T("kit.uninstall_keep", "path", s.Path)
						}
						mono(muted(ui.Text(c.Key("pku-"+s.Skill), line))).FontSize(fsSub).Wrap()
					}
				})
			})
			ui.Checkbox(c, &st.uninstallForce, T("kit.uninstall_force"))
			if st.guide != nil && len(st.guide.MCPRemoveCmd) > 0 {
				ui.Checkbox(c, &st.wireMCP, T("kit.unwire_mcp_ask"))
				codeBox(mono(ui.Text(c, strings.Join(st.guide.MCPRemoveCmd, " "))).FontSize(fsCaption).Wrap())
			}
			if st.guide != nil && st.guide.HasService {
				ui.Checkbox(c, &st.restartSvc, T("kit.restart_svc_ask", "unit", st.guide.ServiceUnit))
			}
			if !st.wireMCP {
				notice(c, "warn", T("kit.uninstall_mcp_warn"))
			}
			if st.actJ.busy {
				a.jobLine(c, &st.actJ)
				return
			}
			if st.actJ.err != nil {
				errorBox(c, st.actJ.err, a.fold("kit-rm-err-"+row.Server+"/"+row.Instance))
			}
			need := T("need.valise")
			if st.wireMCP {
				need = T("need.admin")
			}
			ui.Row(c).Gap(8).AlignItems(ui.Center).Justify(ui.End).Children(func() {
				needTag(c, need)
				ui.Button(c, T("common.cancel")).OnClick(func() {
					st.uninstallOpen = false
					st.actJ.err = nil
				})
				dangerButton(c, T("kit.uninstall_confirm")).OnClick(func() {
					a.runKitUninstall(row.Server, row.Instance, st)
				})
			})
		})
	}).Dismissed() && !st.actJ.busy {
		st.uninstallOpen = false
		st.actJ.err = nil
	}
}

func (a *App) runKitInstall(server, instance string, st *agentKitState) {
	ops := a.kitOps()
	opts := service.KitInstallOptions{WireMCP: st.wireMCP, RestartService: st.restartSvc}
	var plan *service.KitPlan
	a.start(&st.actJ, 2*time.Minute, func(ctx context.Context) (err error) {
		plan, err = ops.installWithOptions(ctx, server, instance, opts, false)
		return err
	}, func(err error) {
		if err != nil {
			return
		}
		st.installOpen = false
		st.lastInstall = plan
		st.lastRemoval = nil
		if plan.NoSkills != "" {
			st.note = T("kit.installed_no_skills", "reason", plan.NoSkills)
		} else if plan.Result != nil {
			st.note = T("kit.installed_note", "written", len(plan.Result.Written), "same", len(plan.Result.Same))
			if len(plan.Result.LegacyRemoved) > 0 {
				st.note += T("kit.legacy_cleaned", "n", len(plan.Result.LegacyRemoved))
			}
		}
		*a.fold("kit-guide-" + server + "/" + instance) = true
		a.readAgentKit(server, instance)
	})
}

func (a *App) runKitUninstall(server, instance string, st *agentKitState) {
	ops := a.kitOps()
	opts := service.KitUninstallOptions{Force: st.uninstallForce, UnwireMCP: st.wireMCP, RestartService: st.restartSvc}
	var rem *service.KitRemovalPlan
	a.start(&st.actJ, 2*time.Minute, func(ctx context.Context) (err error) {
		rem, err = ops.uninstallWithOptions(ctx, server, instance, opts, false)
		return err
	}, func(err error) {
		if err != nil {
			return
		}
		st.uninstallOpen = false
		st.lastRemoval = rem
		st.lastInstall = nil
		st.note = ""
		if kitStepFailed(rem.MCPWire, rem.ServiceRestart) {
			*a.fold("kit-guide-" + server + "/" + instance) = true
		}
		a.readAgentKit(server, instance)
	})
}

func kitStepFailed(mcp *service.KitStepOutcome, svc *service.KitServiceRestartOutcome) bool {
	if mcp != nil && (!mcp.Attempted || !mcp.OK) {
		return true
	}
	return svc != nil && (!svc.Attempted || !svc.OK || !svc.Healthy)
}

func (a *App) kitOutcomeSection(c *ui.Context, st *agentKitState) {
	if st.lastInstall != nil {
		subPanel(ui.Column(c)).Gap(6).Children(func() {
			if st.note != "" {
				notice(c, "ok", st.note)
			}
			if st.lastInstall.Result != nil && len(st.lastInstall.Result.LegacyKept) > 0 {
				notice(c, "warn", T("kit.legacy_kept", "paths", strings.Join(st.lastInstall.Result.LegacyKept, ", ")))
			}
			if st.lastInstall.MCPWire != nil {
				a.kitMCPStepRow(c, st.lastInstall.MCPWire)
			}
			if st.lastInstall.ServiceRestart != nil {
				a.kitRestartRows(c, st.lastInstall.ServiceRestart)
			}
		})
	}
	if st.lastRemoval != nil && st.lastRemoval.Result != nil {
		rem := st.lastRemoval
		subPanel(ui.Column(c)).Gap(6).Children(func() {
			ui.Text(c, T("kit.removed_summary", "removed", len(rem.Result.Removed), "kept", len(rem.Result.Kept))).FontSize(fsSub).Wrap()
			if rem.MCPWire != nil {
				a.kitMCPStepRow(c, rem.MCPWire)
			}
			if rem.ServiceRestart != nil {
				a.kitRestartRows(c, rem.ServiceRestart)
			}
			if (rem.MCPWire == nil || !rem.MCPWire.OK) && (len(rem.MCPCommand) > 0 || rem.MCPSnippet) {
				if len(rem.MCPCommand) > 0 || rem.MCPSnippet {
					muted(ui.Text(c, T("kit.unwire_note"))).FontSize(fsSub).Wrap()
				}
				if len(rem.MCPCommand) > 0 {
					cmd := strings.Join(rem.MCPCommand, " ")
					a.copyableCode(c, st, cmd, T("kit.unwire_note"), T("kit.copy_remove_cmd"))
				} else if rem.MCPSnippet {
					muted(ui.Text(c, T("kit.unwire_snippet"))).FontSize(fsSub).Wrap()
				}
			}
		})
	}
}

func (a *App) kitMCPStepRow(c *ui.Context, m *service.KitStepOutcome) {
	switch {
	case !m.Attempted:
		statusRow(c, "warn", T("kit.outcome.mcp"), orText(m.Err, T("kit.outcome.not_run")))
	case m.OK:
		statusRow(c, "ok", T("kit.outcome.mcp"), T("kit.outcome.mcp_ok", "code", m.ExitCode))
	default:
		statusRow(c, "warn", T("kit.outcome.mcp"), T("kit.outcome.mcp_fail", "code", m.ExitCode, "err", m.Err))
	}
	if out := truncateLines(m.Output, 6); out != "" {
		codeBox(mono(ui.Text(c, out)).FontSize(fsCaption).Wrap())
	}
}

func (a *App) kitRestartRows(c *ui.Context, r *service.KitServiceRestartOutcome) {
	switch {
	case !r.Attempted:
		statusRow(c, "warn", T("kit.outcome.restart"), orText(r.Err, T("kit.outcome.not_run")))
	case r.OK:
		statusRow(c, "ok", T("kit.outcome.restart"), T("kit.outcome.restart_ok", "unit", r.Unit, "code", r.ExitCode))
	default:
		statusRow(c, "warn", T("kit.outcome.restart"), T("kit.outcome.restart_fail", "unit", r.Unit, "code", r.ExitCode, "err", r.Err))
	}
	if !r.Attempted {
		return
	}
	switch {
	case r.Healthy:
		statusRow(c, "ok", T("kit.outcome.health"), T("kit.outcome.health_ok", "unit", r.Unit))
	case r.OK:
		statusRow(c, "warn", T("kit.outcome.health"), T("kit.outcome.health_unverified", "unit", r.Unit))
	default:
		statusRow(c, "neutral", T("kit.outcome.health"), T("kit.outcome.health_skipped"))
	}
}

func truncateLines(s string, maxLines int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	out := strings.Join(lines, "\n")
	if len(out) > 400 {
		out = out[len(out)-400:]
	}
	return strings.TrimSpace(out)
}

func skillStateMeta(s agentkit.SkillState) (tone, text string) {
	switch s.State {
	case agentkit.SkillCurrent:
		tone, text = "ok", T("kit.skill.current")
	case agentkit.SkillModified:
		tone, text = "warn", T("kit.skill.modified")
	default:
		tone, text = "neutral", T("kit.skill.missing")
	}
	if s.Backups > 0 {
		text += T("kit.skill.backups", "n", s.Backups)
	}
	return tone, text
}
