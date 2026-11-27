package gui

import (
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"

	"valise/core/revive"
	"valise/ui/service"
)

func (a *App) resolveOriginHost(origin string, src service.RestoreSource) string {
	hosts := a.hosts()
	match := func(cand string) string {
		if cand == "" {
			return ""
		}
		if slices.Contains(hosts, cand) {
			return cand
		}
		for _, host := range hosts {
			if a.vaultNames[host] == cand {
				return host
			}
		}
		for host, name := range a.vaultNames {
			if name == cand {
				return host
			}
		}
		return ""
	}
	if origin != "" {
		if h := match(origin); h != "" {
			return h
		}
		return origin
	}
	server, _, _ := strings.Cut(src.Copy.SourceLocator, "/")
	if h := match(server); h != "" {
		return h
	}
	if src.Spec == "" {
		if h := match(src.Copy.LocationRef.ID); h != "" {
			return h
		}
		return src.Copy.LocationRef.ID
	}
	return ""
}

func (a *App) restoreSetup(c *ui.Context, r *restoreFlow) {
	if r.j.busy {
		a.restoreProgress(c, r)
		return
	}
	if len(r.point.BundleMembers) > 1 && r.bundleErr == nil {
		card(ui.Column(c)).Gap(spInside).Border(1, palette.TroubleBorder).Background(palette.TroubleBg).Children(func() {
			notice(c, "warn", T("bundle.point_badge", "members", otherBundleMembers(r.point)))
			ui.Row(c).Justify(ui.End).Children(func() {
				ui.Button(c, T("bundle.switch_restore")).OnClick(func() {
					a.openBundleRestoreFromFlow(r, r.point.BundleID, r.point.BundleMembers)
				})
			})
		})
	}
	card(ui.Column(c)).Gap(10).Children(func() {
		ui.Text(c, T("restore.how")).Bold()
		ui.RadioGroup(c, func() {
			for _, m := range []string{revive.ModeRollback, revive.ModeMigrate, revive.ModeClone} {
				if ui.Radio(c.Key(m), &r.mode, m, T("restore.mode."+m)).Changed() {
					r.resetPlanOptIns()
					// A copy starts on the agent's own server (docs/UI.md
					// 7.11); only a migration needs another one.
					switch {
					case m != revive.ModeMigrate:
						r.target = r.origin
					case r.target == r.origin:
						r.target = ""
					}
				}
				muted(ui.Text(c, T("restore.mode_hint."+m))).Wrap()
			}
		}).Label(T("restore.how"))
	})
	card(ui.Column(c)).Gap(8).Children(func() {
		if len(r.sources) == 1 {
			fact(c, T("restore.read_from"), journeyLocation(r.sources[0].Copy.LocationRef))
		} else {
			ui.Text(c, T("restore.read_from")).Bold()
			ui.RadioGroup(c, func() {
				selected := r.sources[r.source].Copy.LocationRef.Kind + "/" + r.sources[r.source].Copy.LocationRef.ID
				for i, src := range r.sources {
					key := src.Copy.LocationRef.Kind + "/" + src.Copy.LocationRef.ID
					if ui.Radio(c.Key(key), &selected, key, journeyLocation(src.Copy.LocationRef)).Changed() {
						r.source = i
						r.resetPlanOptIns()
					}
				}
			}).Label(T("restore.read_from"))
		}
		muted(ui.Text(c, T("restore.source_note"))).Wrap()
	})
	card(ui.Column(c)).Gap(10).Children(func() {
		ui.Text(c, T("restore.where")).Bold()
		switch r.mode {
		case revive.ModeRollback:
			fact(c, T("restore.target"), orText(r.origin, T("restore.origin_unknown")))
		case revive.ModeMigrate:
			hosts := restoreTargets(a.hosts(), r.origin, r.mode)
			if r.target == r.origin || (r.target != "" && !slices.Contains(hosts, r.target)) {
				r.target = ""
			}
			if r.target == "" && len(hosts) == 1 {
				r.target = hosts[0]
			}
			if len(hosts) == 0 {
				notice(c, "warn", T("restore.migrate_no_other_server"))
			} else {
				labeled(c, T("restore.target"), func() {
					if ui.Select(c, &r.target, hosts).Label(T("restore.target")).Changed() {
						r.resetPlanOptIns()
					}
				})
			}
		case revive.ModeClone:
			hosts := restoreTargets(a.hosts(), r.origin, r.mode)
			if r.target == "" && len(hosts) > 0 {
				if slices.Contains(hosts, r.origin) {
					r.target = r.origin
				} else {
					r.target = hosts[0]
				}
			}
			labeled(c, T("restore.target"), func() {
				if ui.Select(c, &r.target, hosts).Label(T("restore.target")).Changed() {
					r.resetPlanOptIns()
				}
			})
			labeled(c, T("restore.copy_id"), func() { ui.TextInput(c, &r.id).Label(T("restore.copy_id")) })
		}
		a.restorePlacementUI(c, r)
	})
	muted(ui.Text(c, T("restore.plan_note"))).Wrap()
	disablePlan := r.mode == revive.ModeMigrate && (r.target == "" || r.target == r.origin)
	ui.Row(c).Justify(ui.End).Children(func() {
		ui.PrimaryButton(c, T("restore.view_plan")).Disabled(disablePlan).OnClick(func() { a.needKey(func() { a.prepareRestore(r) }) })
	})
}

// restoreTargets are the servers a restore may go to: a migration leaves
// the origin, a copy may stay on it.
func restoreTargets(all []string, origin, mode string) []string {
	var hosts []string
	for _, h := range all {
		if h != origin || mode != revive.ModeMigrate {
			hosts = append(hosts, h)
		}
	}
	return hosts
}

func (a *App) restorePlan(c *ui.Context, r *restoreFlow) {
	if r.j.busy {
		a.restoreProgress(c, r)
		return
	}
	if r.plan == nil {
		return
	}
	p := r.plan.Revive
	if r.envAutoSkipped {
		notice(c, "ok", T("restore.env.auto_skipped_note"))
	}
	for i, w := range p.Warnings { // warnings come first and are never secondary text
		notice(c.Key(i), "warn", w)
	}
	card(ui.Column(c)).Gap(8).Children(func() {
		ui.Text(c, T("restore.plan")).Bold()
		fact(c, T("restore.origin"), p.From)
		fact(c, T("restore.read_from"), journeyLocation(r.sources[r.source].Copy.LocationRef))
		fact(c, T("restore.target"), p.Target+" / "+p.Plan.InstanceID)
		fact(c, T("point.scope"), tierText(p.Plan.Tier))
		fact(c, T("restore.data"), T("restore.data."+p.Plan.Mode))
		fact(c, T("restore.replaces"), orText(strings.Join(p.Plan.Supersedes, ", "), T("restore.none")))
		fact(c, T("restore.shared"), orText(strings.Join(p.Plan.Keep, ", "), T("restore.none")))
		if p.Plan.Unit == "" {
			fact(c, T("restore.services"), T("restore.no_service"))
		} else {
			fact(c, T("restore.services"), T("restore.service_effect", "unit", p.Plan.Unit, "source", orText(p.StopSource, T("restore.none"))))
			if p.Plan.InstallService {
				fact(c, T("restore.service_install_label"), T("restore.service_auto_install", "unit", p.Plan.Unit, "cmd", strings.Join(p.InstallArgv, " ")))
			}
		}
		fact(c, T("restore.space"), T("restore.space_value", "need", size(p.NeedBytes), "free", size(int64(p.FreeBytes))))
		bodyVal := T("restore.body." + p.Plan.Body.Action)
		if p.BodySummary != "" {
			bodyVal += T("common.dot_sep") + p.BodySummary
		}
		fact(c, T("restore.body"), bodyVal)
		if p.Dossier != nil {
			fact(c, T("restore.dossier"), T("restore.dossier_value", "overrides", p.Dossier.LocalOverrides, "sqlite", len(p.Dossier.LocalSQLite), "external", len(p.Dossier.ExternalRefs)))
			if len(p.Dossier.LocalRoots) > 0 {
				var roots []string
				for _, lr := range p.Dossier.LocalRoots {
					roots = append(roots, lr.Path)
				}
				fact(c, T("restore.dossier_local_roots"), T("restore.dossier_local_roots_val", "n", len(p.Dossier.LocalRoots), "list", strings.Join(roots, T("common.list_sep"))))
			}
		}
		if tc := p.ToolCheck; tc != nil && (len(tc.Present) > 0 || len(tc.Missing) > 0) {
			if len(tc.Missing) > 0 {
				var names []string
				for _, td := range tc.Missing {
					names = append(names, td.Name)
				}
				fact(c, T("restore.tool_check_label"), T("restore.tool_check_summary_missing", "present", len(tc.Present), "missing", len(tc.Missing), "tools", strings.Join(names, T("common.list_sep"))))
			} else {
				fact(c, T("restore.tool_check_label"), T("restore.tool_check_summary_ok", "present", len(tc.Present), "tools", strings.Join(tc.Present, T("common.list_sep"))))
			}
		}
		fact(c, T("restore.access"), T("restore.access_hint"))
	})
	ui.Collapsible(c, T("restore.technical"), a.fold("restore-technical"), func() {
		for _, root := range p.Plan.Roots {
			fact(c, root.ID, root.Path)
		}
		mono(muted(ui.Text(c, p.Plan.OpID))).Wrap().Selectable()
		for _, cmd := range service.RestoreCommands(p) {
			mono(ui.Text(c, cmd)).Wrap().Selectable()
		}
		for _, file := range p.Excluded {
			mono(muted(ui.Text(c, file))).Wrap().Selectable()
		}
	})
	if p.RestoreSecret || len(p.Excluded) > 0 {
		ui.Text(c, T("restore.secrets")).Bold().TextColor(palette.Warn)
		key := "restore.secret_restore_hint"
		if r.mode == revive.ModeClone {
			key = "restore.secret_clone_hint"
		}
		muted(ui.Text(c, T(key))).Wrap()
		ui.Checkbox(c, &r.credential, T("restore.secret_confirm"))
	} else {
		muted(ui.Text(c, T("restore.without_secrets"))).Wrap()
	}
	if u := p.UnverifiedSource; u != nil {
		card(ui.Column(c)).Gap(6).Children(func() {
			ui.Text(c, T("restore.unverified")).Bold().TextColor(palette.Warn)
			muted(ui.Text(c, T("restore.unverified_info", "id", u.InstanceID, "profile", u.ProfileID, "version", u.ProfileVersion, "server", u.SourceServer, "period", u.Period, "time", u.CreatedAt.Local().Format("2006-01-02 15:04"), "tier", tierText(u.Tier)))).Wrap().Selectable()
			ui.RadioGroup(c, func() {
				ui.Radio(c, &r.sourceChoice, "proceed", T("restore.proceed"))
				if u.BackupFirst {
					ui.Radio(c, &r.sourceChoice, "backup_first", T("restore.backup_first"))
					muted(ui.Text(c, T("restore.backup_first_hint"))).Wrap()
				}
			}).Label(T("restore.unverified"))
		})
	}
	if r.mode == revive.ModeRollback {
		ui.Checkbox(c, &r.backupCurrent, T("restore.backup_current"))
		muted(ui.Text(c, T("restore.backup_current_hint"))).Wrap()
	}
	if r.mode == revive.ModeMigrate && p.SourceProtection != nil {
		ui.Checkbox(c, &r.carryProtection, T("restore.carry_protection", "summary", p.SourceProtection.Summary))
	}
	a.restoreHomePathsSection(c, r, p)
	allow, hasEnv := false, false
	for _, block := range p.Blockers {
		b := block
		switch b.Code {
		case "profile_draft":
			allow = true
			subPanel(ui.Column(c.Key("blk-" + b.Code))).Gap(6).Children(func() {
				ui.Checkbox(c, &r.draft, T("restore.allow_draft"))
				if b.Reason != "" {
					muted(ui.Text(c, b.Reason)).FontSize(fsCaption).Wrap().Selectable()
				}
			})
		case "profile_from_snapshot":
			allow = true
			subPanel(ui.Column(c.Key("blk-" + b.Code))).Gap(6).Children(func() {
				ui.Checkbox(c, &r.snapshotProfile, T("restore.allow_snapshot"))
				if b.Reason != "" {
					muted(ui.Text(c, b.Reason)).FontSize(fsCaption).Wrap().Selectable()
				}
			})
		default:
			hasEnv = true
			notice(c.Key("blk-"+b.Code), "warn", b.Reason)
		}
	}
	if len(p.Nested) > 0 {
		hasEnv = true
		notice(c, "warn", T("restore.nested", "names", strings.Join(p.Nested, ", ")))
	}
	ui.Row(c).Gap(10).Justify(ui.End).Children(func() {
		ui.Button(c, T("restore.edit")).OnClick(func() {
			r.resetPlanOptIns()
			r.step = "setup"
		})
		if hasEnv {
			ui.PrimaryButton(c, T("restore.env.open")).OnClick(func() { r.step = "env" })
		}
		if allow {
			ui.Button(c, T("restore.replan")).OnClick(func() { a.prepareRestore(r) })
		}
		if len(p.Blockers) == 0 && len(p.Nested) == 0 {
			ui.PrimaryButton(c, T("restore.prepare")).Disabled((p.RestoreSecret || len(p.Excluded) > 0) && !r.credential || p.UnverifiedSource != nil && r.sourceChoice == "").OnClick(func() { a.runRestore(r) })
		}
	})
}

// Legacy locale key "restore.secret_hint" remains for imported catalogs.
