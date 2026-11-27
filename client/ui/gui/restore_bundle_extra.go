package gui

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/revive"
	"valise/ui/service"
)

type memberRewireState struct {
	j        job
	plan     *service.KitPlan
	copyNote string
}

// renderBatchMigrateOptions renders external symlink preview and single-agent-
// parity migration carryover controls (protection, home symlinks, MCP, config
// home path rewrites) for bundle restore and one-click move-all plans.
func (a *App) renderBatchMigrateOptions(c *ui.Context, keyPrefix, mode string, sum batchReviveSummary, opts *batchReviveOpts) {
	if len(sum.extSymlinks) > 0 {
		subPanel(ui.Column(c.Key(keyPrefix + "-ext-sym"))).Gap(4).Children(func() {
			muted(ui.Text(c, T("restore.ext_symlinks.warn", "n", len(sum.extSymlinks)))).FontSize(fsSub).Wrap()
			ui.Collapsible(c, T("restore.ext_symlinks.list", "n", len(sum.extSymlinks)), a.fold(keyPrefix+"-ext-symlinks"), func() {
				for i, s := range sum.extSymlinks {
					line := s.instanceID + ": " + s.root + "/" + s.path + " -> " + s.target
					mono(muted(ui.Text(c.Key(keyPrefix+"-es-"+strconv.Itoa(i)), line))).FontSize(fsCaption).Wrap().Selectable()
				}
			})
		})
	}
	if mode != revive.ModeMigrate {
		return
	}
	if len(sum.sourceProtections) > 0 {
		ui.Checkbox(c.Key(keyPrefix+"-carry-prot"), &opts.carryProtection, T("restore.carry_protection", "summary", strings.Join(sum.sourceProtections, T("common.list_sep"))))
	}
	if sum.hasHomeChanged {
		ui.Checkbox(c.Key(keyPrefix+"-remap-home"), &opts.remapHomeSymlinks, T("restore.home.remap_symlinks", "old", sum.sourceHome, "new", sum.targetHome))
	}
	if sum.hasMCP {
		ui.Row(c.Key(keyPrefix + "-carry-mcp-row")).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
			needTag(c, T("need.admin"))
			ui.Checkbox(c.Key(keyPrefix+"-carry-mcp"), &opts.carryMCP, T("restore.home.carry_mcp"))
		})
	}
	if sum.hasHomeChanged && len(sum.configHomeHits) > 0 {
		subPanel(ui.Column(c.Key(keyPrefix + "-rewrite-cfg-box"))).Gap(6).Children(func() {
			ui.Checkbox(c.Key(keyPrefix+"-rewrite-cfg"), &opts.rewriteConfigHomePaths, T("restore.home.rewrite_config", "n", len(sum.configHomeHits)))
			ui.Scroll(c).MaxHeight(160).Children(func() {
				codeBox(ui.Column(c)).Gap(3).Children(func() {
					for i, h := range sum.configHomeHits {
						line := h.instanceID + ": " + h.root + "/" + h.path + ":L" + strconv.Itoa(h.line) + ": " + h.oldPath + " -> " + h.newPath
						mono(muted(ui.Text(c.Key(keyPrefix+"-ch-"+strconv.Itoa(i)), line))).FontSize(fsCaption).Wrap().Selectable()
					}
				})
			})
		})
	}
}

func ensureMemberRewire(m *map[string]*memberRewireState, instance string) *memberRewireState {
	if *m == nil {
		*m = map[string]*memberRewireState{}
	}
	if (*m)[instance] == nil {
		(*m)[instance] = &memberRewireState{}
	}
	return (*m)[instance]
}

// renderMemberPostMigrate displays a single member's PostMigrateReport inside
// bundle restore or one-click move-all results, matching single-agent migrate.
func (a *App) renderMemberPostMigrate(c *ui.Context, keyPrefix, target, instance string, pm *service.PostMigrateReport, rewires *map[string]*memberRewireState) {
	if pm == nil {
		return
	}
	st := ensureMemberRewire(rewires, instance)
	subPanel(ui.Column(c.Key(keyPrefix + "-pm-" + instance))).Gap(spInside).Children(func() {
		ui.Text(c, T("bundle.post.member_title", "id", instance)).FontSize(fsSub).Bold()
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
			for _, lr := range pm.LocalRoots {
				ui.Row(c.Key(keyPrefix + "-lr-" + instance + "-" + lr.ID)).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
					mono(ui.Text(c, lr.ID)).FontSize(fsSub).Selectable()
					mono(muted(ui.Text(c, lr.Path))).FontSize(fsSub).Grow(1).Wrap().Selectable()
					if lbl := proposalCategoryLabel(lr.Category); lbl != "" {
						plainTag(c, lbl)
					} else if lr.Category != "" {
						plainTag(c, lr.Category)
					}
				})
			}
		}

		if pm.RemappedSymlinks > 0 {
			statusRow(c, "ok", T("restore.post.remapped_symlinks"), T("restore.post.remapped_symlinks_val", "n", pm.RemappedSymlinks))
		}
		if pm.RewrittenConfigPaths > 0 {
			statusRow(c, "ok", T("restore.post.rewritten_paths"), T("restore.post.rewritten_paths_val", "n", pm.RewrittenConfigPaths))
		}
		if pm.RemainingConfigHomeHits > 0 {
			notice(c, "warn", T("restore.post.remaining_home_hits", "n", pm.RemainingConfigHomeHits))
		}

		a.renderMemberPostMCP(c, keyPrefix, target, instance, pm, st)
		a.renderMemberPostGaps(c, keyPrefix, instance, pm, st)
	})
}

func (a *App) renderMemberPostMCP(c *ui.Context, keyPrefix, target, instance string, pm *service.PostMigrateReport, st *memberRewireState) {
	showRetry := false
	switch {
	case pm.MCPAutoRan && pm.MCPWireResult != nil:
		ui.Column(c.Key(keyPrefix + "-mcp-auto-" + instance)).Gap(6).Children(func() {
			a.kitMCPStepRow(c, pm.MCPWireResult)
			if pm.MCPServiceRestart != nil {
				a.kitRestartRows(c, pm.MCPServiceRestart)
			}
		})
		showRetry = pm.MCPWireOutcome == "failed" && pm.MCPCanWire
	case pm.MCPAutoRan && pm.MCPWireOutcome == "wired":
		statusRow(c, "ok", T("restore.post.mcp"), orText(pm.MCPNote, T("restore.post.mcp_auto_wired")))
	case pm.MCPAutoRan && pm.MCPWireOutcome == "failed":
		statusRow(c, "warn", T("restore.post.mcp"), orText(pm.MCPNote, T("restore.post.mcp_auto_failed")))
		showRetry = pm.MCPCanWire
	case pm.MCPChecked && pm.MCPWired && !pm.MCPNeedsRewire:
		statusRow(c, "ok", T("restore.post.mcp"), orText(pm.MCPNote, T("restore.post.mcp_ok")))
	case pm.MCPCanWire && (pm.MCPNeedsRewire || !pm.MCPWired):
		statusRow(c, "warn", T("restore.post.mcp"), orText(pm.MCPNote, T("restore.post.mcp_needs_rewire")))
		showRetry = true
	case pm.MCPChecked || pm.MCPNote != "":
		tone := "neutral"
		if pm.MCPNeedsRewire {
			tone = "warn"
		}
		statusRow(c, tone, T("restore.post.mcp"), orText(pm.MCPNote, T("restore.post.mcp_unknown")))
	}
	if showRetry {
		ui.Row(c.Key(keyPrefix + "-rewire-" + instance)).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
			needTag(c, T("need.admin"))
			ui.Button(c, T("restore.post.rewire_mcp")).Disabled(st.j.busy).OnClick(func() {
				a.runMemberMCPRewire(st, target, instance, pm)
			})
		})
	}
	if st.j.busy {
		a.jobLine(c, &st.j)
	}
	if st.j.err != nil {
		errorBox(c, st.j.err, a.fold(keyPrefix+"-rewire-err-"+instance))
	}
	if st.plan != nil {
		ui.Column(c.Key(keyPrefix + "-mcp-retry-" + instance)).Gap(6).Children(func() {
			if st.plan.MCPWire != nil {
				a.kitMCPStepRow(c, st.plan.MCPWire)
			}
			if st.plan.ServiceRestart != nil {
				a.kitRestartRows(c, st.plan.ServiceRestart)
			}
		})
	}
}

func (a *App) renderMemberPostGaps(c *ui.Context, keyPrefix, instance string, pm *service.PostMigrateReport, st *memberRewireState) {
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
		var names []string
		for _, td := range pm.ToolMissing {
			names = append(names, td.Name)
			ui.Row(c.Key(keyPrefix + "-tm-" + instance + "-" + td.Name)).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
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
						st.copyNote = T("restore.post.copy_failed")
					} else {
						st.copyNote = T("restore.post.copied_tools")
					}
				})
				if st.copyNote != "" {
					muted(ui.Text(c, st.copyNote)).FontSize(fsCaption)
				}
			})
		}
	}
	if len(pm.ExternalRefs) > 0 {
		statusRow(c, "warn", T("restore.post.external_refs"), T("restore.post.external_refs_note", "n", len(pm.ExternalRefs)))
		for _, ref := range pm.ExternalRefs {
			ui.Row(c.Key(keyPrefix + "-ext-" + instance + "-" + ref.Path)).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
				mono(ui.Text(c, ref.Path)).FontSize(fsSub).Selectable()
				if ref.Reason != "" {
					muted(ui.Text(c, ref.Reason)).FontSize(fsSub).Grow(1).Wrap().Selectable()
				}
			})
		}
	}
}

func (a *App) runMemberMCPRewire(st *memberRewireState, target, instance string, pm *service.PostMigrateReport) {
	if st.j.busy || target == "" || instance == "" {
		return
	}
	fn := a.rewireMigratedMCP
	if fn == nil {
		fn = a.svc.RewireMigratedMCP
	}
	var plan *service.KitPlan
	a.start(&st.j, 2*time.Minute, func(ctx context.Context) (err error) {
		plan, err = fn(ctx, target, instance)
		return err
	}, func(err error) {
		if err == nil && plan != nil {
			st.plan = plan
			if pm != nil && plan.MCPWire != nil && plan.MCPWire.OK {
				pm.MCPWired = true
				pm.MCPNeedsRewire = false
				pm.MCPWireOutcome = "wired"
			}
		}
	})
}
