package gui

import (
	"context"
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
		dialog(ui.Column(c), 500).Children(func() {
			dialogTitle(ui.Text(c, T("kit.install_title")))
			ui.Text(c, T("kit.install_what", "dir", st.status.Dir)).Wrap()
			hasMod := false
			ui.Scroll(c).MaxHeight(220).Children(func() {
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
			if st.actJ.busy {
				a.jobLine(c, &st.actJ)
				return
			}
			if st.actJ.err != nil {
				errorBox(c, st.actJ.err, a.fold("kit-inst-err-"+row.Server+"/"+row.Instance))
			}
			ui.Row(c).Gap(8).AlignItems(ui.Center).Justify(ui.End).Children(func() {
				needTag(c, T("need.valise"))
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
		dialog(ui.Column(c), 500).Children(func() {
			dialogTitle(ui.Text(c, T("kit.uninstall_title")))
			ui.Text(c, T("kit.uninstall_what")).Wrap()
			ui.Scroll(c).MaxHeight(200).Children(func() {
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
			notice(c, "warn", T("kit.uninstall_mcp_warn"))
			if st.actJ.busy {
				a.jobLine(c, &st.actJ)
				return
			}
			if st.actJ.err != nil {
				errorBox(c, st.actJ.err, a.fold("kit-rm-err-"+row.Server+"/"+row.Instance))
			}
			ui.Row(c).Gap(8).AlignItems(ui.Center).Justify(ui.End).Children(func() {
				needTag(c, T("need.valise"))
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
	var plan *service.KitPlan
	a.start(&st.actJ, 2*time.Minute, func(ctx context.Context) (err error) {
		plan, err = ops.install(ctx, server, instance, false)
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
	var rem *service.KitRemovalPlan
	a.start(&st.actJ, 2*time.Minute, func(ctx context.Context) (err error) {
		rem, err = ops.uninstall(ctx, server, instance, st.uninstallForce, false)
		return err
	}, func(err error) {
		if err != nil {
			return
		}
		st.uninstallOpen = false
		st.lastRemoval = rem
		st.lastInstall = nil
		st.note = ""
		a.readAgentKit(server, instance)
	})
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
