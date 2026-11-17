package gui

import (
	"github.com/egoist/mygo/ui"
	"strings"
	"valise/core/revive"
	"valise/ui/service"
)

func (a *App) restoreSetup(c *ui.Context, r *restoreFlow) {
	if r.j.busy {
		a.restoreProgress(c, r)
		return
	}
	card(ui.Column(c)).Gap(10).Children(func() {
		ui.Text(c, T("restore.how")).Bold()
		ui.RadioGroup(c, func() {
			for _, m := range []string{revive.ModeRollback, revive.ModeMigrate, revive.ModeClone} {
				if ui.Radio(c.Key(m), &r.mode, m, T("restore.mode."+m)).Changed() {
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
		ui.Text(c, T("restore.read_from")).Bold()
		ui.RadioGroup(c, func() {
			selected := r.sources[r.source].Copy.LocationRef.Kind + "/" + r.sources[r.source].Copy.LocationRef.ID
			for i, src := range r.sources {
				key := src.Copy.LocationRef.Kind + "/" + src.Copy.LocationRef.ID
				if ui.Radio(c.Key(key), &selected, key, journeyLocation(src.Copy.LocationRef)).Changed() {
					r.source = i
				}
			}
		}).Label(T("restore.read_from"))
		muted(ui.Text(c, T("restore.source_note"))).Wrap()
	})
	card(ui.Column(c)).Gap(10).Children(func() {
		ui.Text(c, T("restore.where")).Bold()
		if r.mode != revive.ModeRollback {
			labeled(c, T("restore.origin"), func() { ui.Select(c, &r.origin, a.hosts()).Label(T("restore.origin")) })
		}
		if r.mode == revive.ModeRollback {
			fact(c, T("restore.target"), r.origin)
		} else {
			hosts := restoreTargets(a.hosts(), r.origin, r.mode)
			labeled(c, T("restore.target"), func() { ui.Select(c, &r.target, hosts).Label(T("restore.target")) })
		}
		if r.mode == revive.ModeClone {
			labeled(c, T("restore.copy_id"), func() { ui.TextInput(c, &r.id).Label(T("restore.copy_id")) })
		}
		roots := func() {
			labeled(c, T("restore.root"), func() {
				ui.TextArea(c, &r.roots).Height(90).Label(T("restore.root")).Placeholder(T("restore.root_example"))
			})
		}
		switch {
		case r.mode == revive.ModeRollback:
		case r.mode == revive.ModeClone && r.target == r.origin:
			// A copy beside the original needs its own directories, so
			// the mapping is asked for outright (docs/UI.md 7.11).
			ui.Text(c, T("restore.path_mapping")).Bold()
			muted(ui.Text(c, T("restore.same_server_hint"))).Wrap()
			roots()
		default:
			ui.Collapsible(c, T("restore.path_mapping"), a.fold("restore-roots"), func() {
				muted(ui.Text(c, T("restore.path_hint"))).Wrap()
				roots()
			})
		}
	})
	muted(ui.Text(c, T("restore.plan_note"))).Wrap()
	ui.Row(c).Justify(ui.End).Children(func() {
		ui.PrimaryButton(c, T("restore.view_plan")).OnClick(func() { a.needKey(func() { a.prepareRestore(r) }) })
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
	if r.plan == nil {
		return
	}
	p := r.plan.Revive
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
		}
		fact(c, T("restore.space"), T("restore.space_value", "need", size(p.NeedBytes), "free", size(int64(p.FreeBytes))))
		fact(c, T("restore.body"), T("restore.body."+p.Plan.Body.Action))
		fact(c, T("restore.access"), T("restore.access_hint"))
	})
	ui.Collapsible(c, T("restore.technical"), a.fold("restore-technical"), func() {
		for _, root := range p.Plan.Roots {
			fact(c, root.ID, root.Path)
		}
		mono(muted(ui.Text(c, p.Plan.OpID))).Wrap()
		for _, cmd := range service.RestoreCommands(p) {
			mono(ui.Text(c, cmd)).Wrap()
		}
		for _, file := range p.Excluded {
			mono(muted(ui.Text(c, file))).Wrap()
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
			muted(ui.Text(c, T("restore.unverified_info", "id", u.InstanceID, "profile", u.ProfileID, "version", u.ProfileVersion, "server", u.SourceServer, "period", u.Period, "time", u.CreatedAt.Local().Format("2006-01-02 15:04"), "tier", tierText(u.Tier)))).Wrap()
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
	hard, allow := false, false
	for _, block := range p.Blockers {
		switch block.Code {
		case "profile_draft":
			ui.Checkbox(c, &r.draft, T("restore.allow_draft"))
			allow = true
		case "profile_from_snapshot":
			ui.Checkbox(c, &r.snapshotProfile, T("restore.allow_snapshot"))
			allow = true
		default:
			hard = true
		}
		errorBox(c, &block, a.fold("restore-block-"+block.Code))
	}
	if len(p.Nested) > 0 {
		hard = true
		ui.Text(c, T("restore.nested", "names", strings.Join(p.Nested, ", "))).Wrap().TextColor(palette.Warn)
	}
	ui.Row(c).Gap(10).Justify(ui.End).Children(func() {
		ui.Button(c, T("restore.edit")).OnClick(func() { r.step = "setup" })
		if allow {
			ui.Button(c, T("restore.replan")).OnClick(func() { a.prepareRestore(r) })
		}
		if !hard && !allow {
			ui.PrimaryButton(c, T("restore.prepare")).Disabled((p.RestoreSecret || len(p.Excluded) > 0) && !r.credential || p.UnverifiedSource != nil && r.sourceChoice == "").OnClick(func() { a.runRestore(r) })
		}
	})
}

// Legacy locale key "restore.secret_hint" remains for imported catalogs.
