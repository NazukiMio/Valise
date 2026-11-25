package gui

import (
	"context"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/revive"
	"valise/ui/service"
)

func (a *App) restoreResult(c *ui.Context, r *restoreFlow) {
	t := r.task
	if r.j.busy {
		a.restoreProgress(c, r)
		return
	}
	card(ui.Column(c)).Gap(spItem).Children(func() {
		ui.Row(c).Gap(spInside + 2).AlignItems(ui.Center).Children(func() {
			tone := restoreResultTone(t)
			toneDot(c, tone)
			title := dialogTitle(ui.Text(c, T("restore.state."+orText(t.State, "failed")))).Wrap()
			if tone == "warn" || tone == "danger" {
				title.TextColor(toneInk(tone))
			}
		})
		fact(c, T("restore.target"), t.Target+" / "+t.Instance)
		ui.Divider(c)
		ui.Column(c).Gap(spInside).Children(func() {
			for _, o := range restoreOutcomes(t) {
				statusRow(c, o.tone, o.label, o.value)
			}
		})
		if t.Rollback != nil && t.Rollback.Problem != "" {
			notice(c, "warn", T("restore.rollback.problem."+t.Rollback.Problem))
		}
		ui.Collapsible(c, T("restore.kept"), a.fold("restore-kept"), func() {
			for _, path := range t.Prev {
				mono(muted(ui.Text(c, path))).Wrap().Selectable()
			}
		})
		if t.SwapState == revive.StateRolledBack {
			notice(c, "warn", T("restore.rollback_hint"))
		}
		if t.State == "needs_recovery" || t.State == "reconciling" {
			notice(c, "warn", T("restore.reconcile_hint"))
		}
		ui.Row(c).Gap(8).Wrap().Children(func() {
			if t.State == "succeeded" {
				if t.Target != "" && t.Instance != "" {
					ui.PrimaryButton(c, T("restore.open_target_agent")).OnClick(func() {
						a.refreshAgents()
						delete(a.agentServices, t.Target+"/"+t.Instance)
						a.agentTab, a.point = tabOverview, ""
						a.navigate("/agents/" + t.Target + "/" + t.Instance)
					})
				}
				ui.Button(c, T("nav.agents")).OnClick(func() { a.navigate("/agents") })
			}
			if t.OpID != "" && (t.State == "reconciling" || t.State == "needs_recovery" || t.State == "") {
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
	if t.State == "succeeded" && t.PostMigrate != nil {
		a.postMigrateCard(c, r, t, t.PostMigrate)
	}
}

func (a *App) postMigrateCard(c *ui.Context, r *restoreFlow, t service.RestoreTask, pm *service.PostMigrateReport) {
	card(ui.Column(c)).Gap(spItem).Children(func() {
		cardTitle(ui.Text(c, T("restore.post.title")))
		switch {
		case pm.ProtectionAttempted && pm.ProtectionCarried:
			statusRow(c, "ok", T("restore.post.protection"), orText(pm.ProtectionNote, T("restore.post.protection_ok")))
		case pm.ProtectionAttempted:
			statusRow(c, "warn", T("restore.post.protection"), orText(pm.ProtectionNote, T("restore.post.protection_warn")))
		default:
			statusRow(c, "neutral", T("restore.post.protection"), T("restore.post.protection_skipped"))
		}

		if len(pm.LocalRoots) > 0 {
			ui.Text(c, T("restore.post.local_roots")).FontSize(fsSub).Bold()
			subPanel(ui.Column(c)).Gap(6).Children(func() {
				for _, lr := range pm.LocalRoots {
					ui.Row(c.Key("pmlr-" + lr.ID)).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
						mono(ui.Text(c, lr.ID)).FontSize(fsSub).Selectable()
						mono(muted(ui.Text(c, lr.Path))).FontSize(fsSub).Grow(1).Wrap().Selectable()
						if lbl := proposalCategoryLabel(lr.Category); lbl != "" {
							plainTag(c, lbl)
						} else if lr.Category != "" {
							plainTag(c, lr.Category)
						}
					})
				}
			})
		}

		a.postMigrateMCPSection(c, r, t, pm)
		a.postMigrateGapsSection(c, r, pm)
	})
}

func (a *App) postMigrateMCPSection(c *ui.Context, r *restoreFlow, t service.RestoreTask, pm *service.PostMigrateReport) {
	switch {
	case pm.MCPChecked && pm.MCPWired && !pm.MCPNeedsRewire:
		statusRow(c, "ok", T("restore.post.mcp"), orText(pm.MCPNote, T("restore.post.mcp_ok")))
	case pm.MCPCanWire && (pm.MCPNeedsRewire || !pm.MCPWired):
		statusRow(c, "warn", T("restore.post.mcp"), orText(pm.MCPNote, T("restore.post.mcp_needs_rewire")))
		ui.Row(c).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
			needTag(c, T("need.admin"))
			ui.Button(c, T("restore.post.rewire_mcp")).Disabled(r.rewireJ.busy).OnClick(func() {
				a.runPostMigrateMCPRewire(r, t.Target, t.Instance)
			})
		})
	case pm.MCPChecked || pm.MCPNote != "":
		tone := "neutral"
		if pm.MCPNeedsRewire {
			tone = "warn"
		}
		statusRow(c, tone, T("restore.post.mcp"), orText(pm.MCPNote, T("restore.post.mcp_unknown")))
	}
	if r.rewireJ.busy {
		a.jobLine(c, &r.rewireJ)
	}
	if r.rewireJ.err != nil {
		errorBox(c, r.rewireJ.err, a.fold("restore-rewire-err"))
	}
	if r.rewirePlan != nil {
		subPanel(ui.Column(c)).Gap(6).Children(func() {
			if r.rewirePlan.MCPWire != nil {
				a.kitMCPStepRow(c, r.rewirePlan.MCPWire)
			}
			if r.rewirePlan.ServiceRestart != nil {
				a.kitRestartRows(c, r.rewirePlan.ServiceRestart)
			}
		})
	}
}

func (a *App) postMigrateGapsSection(c *ui.Context, r *restoreFlow, pm *service.PostMigrateReport) {
	if len(pm.ToolPresent) == 0 && len(pm.ToolMissing) == 0 && len(pm.ExternalRefs) == 0 {
		return
	}
	ui.Divider(c)
	ui.Text(c, T("restore.post.gaps_title")).FontSize(fsSub).Bold()
	if len(pm.ToolPresent) > 0 {
		statusRow(c, "ok", T("restore.post.tools_present"), strings.Join(pm.ToolPresent, T("common.list_sep")))
	}
	if len(pm.ToolMissing) > 0 {
		statusRow(c, "warn", T("restore.post.tools_missing"), T("restore.post.tools_missing_count", "n", len(pm.ToolMissing)))
		subPanel(ui.Column(c)).Gap(6).Children(func() {
			var names []string
			for _, td := range pm.ToolMissing {
				names = append(names, td.Name)
				ui.Row(c.Key("pmtm-" + td.Name)).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
					mono(ui.Text(c, td.Name)).FontSize(fsSub).Bold().Selectable()
					if td.Purpose != "" {
						muted(ui.Text(c, td.Purpose)).FontSize(fsSub).Grow(1).Wrap().Selectable()
					}
				})
			}
			if a.copyText != nil {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Button(c, T("restore.post.copy_missing_tools")).OnClick(func() {
						if a.copyText(strings.Join(names, " ")) != nil {
							r.postCopyNote = T("restore.post.copy_failed")
						} else {
							r.postCopyNote = T("restore.post.copied_tools")
						}
					})
					if r.postCopyNote != "" {
						muted(ui.Text(c, r.postCopyNote)).FontSize(fsCaption)
					}
				})
			}
		})
	}
	if len(pm.ExternalRefs) > 0 {
		statusRow(c, "warn", T("restore.post.external_refs"), T("restore.post.external_refs_note", "n", len(pm.ExternalRefs)))
		subPanel(ui.Column(c)).Gap(4).Children(func() {
			for i, ref := range pm.ExternalRefs {
				ui.Row(c.Key("pmext-" + ref.Path)).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
					mono(ui.Text(c, ref.Path)).FontSize(fsSub).Selectable()
					if ref.Reason != "" {
						muted(ui.Text(c, ref.Reason)).FontSize(fsSub).Grow(1).Wrap().Selectable()
					}
					_ = i
				})
			}
		})
	}
}

func (a *App) runPostMigrateMCPRewire(r *restoreFlow, target, instance string) {
	if r.rewireJ.busy || target == "" || instance == "" {
		return
	}
	fn := a.rewireMigratedMCP
	if fn == nil {
		fn = a.svc.RewireMigratedMCP
	}
	var plan *service.KitPlan
	a.start(&r.rewireJ, 2*time.Minute, func(ctx context.Context) (err error) {
		plan, err = fn(ctx, target, instance)
		return err
	}, func(err error) {
		if err == nil && plan != nil {
			r.rewirePlan = plan
			if pm := r.task.PostMigrate; pm != nil && plan.MCPWire != nil && plan.MCPWire.OK {
				pm.MCPWired = true
				pm.MCPNeedsRewire = false
			}
		}
	})
}
