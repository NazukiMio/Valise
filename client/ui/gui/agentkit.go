package gui

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/agentkit"
	"valise/ui/service"
)

// The agent's "管理与设置" kit card (docs/AGENT_KIT.md 8.5, docs/UI.md 7.5):
// reads daily-tier status and MCP guide without triggering an owner prompt;
// install and uninstall preview exact paths and confirm before owner actions.

type agentKitOps struct {
	status               func(context.Context, string, string) (*agentkit.KitStatusResult, error)
	guide                func(context.Context, string, string) (*service.KitGuide, error)
	install              func(context.Context, string, string, bool) (*service.KitPlan, error)
	installWithOptions   func(context.Context, string, string, service.KitInstallOptions, bool) (*service.KitPlan, error)
	uninstall            func(context.Context, string, string, bool, bool) (*service.KitRemovalPlan, error)
	uninstallWithOptions func(context.Context, string, string, service.KitUninstallOptions, bool) (*service.KitRemovalPlan, error)
}

type agentKitState struct {
	started        bool
	read           job
	status         *agentkit.KitStatusResult
	guide          *service.KitGuide
	err            error
	actJ           job
	note           string
	copied         string
	installOpen    bool
	uninstallOpen  bool
	uninstallForce bool
	diffSkill      string
	wireMCP        bool
	restartSvc     bool
	lastInstall    *service.KitPlan
	lastRemoval    *service.KitRemovalPlan
}

func (a *App) kitOps() agentKitOps {
	ops := agentKitOps{
		status:               a.svc.KitStatus,
		guide:                a.svc.KitGuide,
		install:              a.svc.KitInstall,
		installWithOptions:   a.svc.KitInstallWithOptions,
		uninstall:            a.svc.KitUninstall,
		uninstallWithOptions: a.svc.KitUninstallWithOptions,
	}
	if h := a.agentKitHook; h != nil {
		if h.status != nil {
			ops.status = h.status
		}
		if h.guide != nil {
			ops.guide = h.guide
		}
		if h.install != nil {
			ops.install = h.install
		}
		if h.installWithOptions != nil {
			ops.installWithOptions = h.installWithOptions
		} else if h.install != nil {
			fn := h.install
			ops.installWithOptions = func(ctx context.Context, host, inst string, _ service.KitInstallOptions, dryRun bool) (*service.KitPlan, error) {
				return fn(ctx, host, inst, dryRun)
			}
		}
		if h.uninstall != nil {
			ops.uninstall = h.uninstall
		}
		if h.uninstallWithOptions != nil {
			ops.uninstallWithOptions = h.uninstallWithOptions
		} else if h.uninstall != nil {
			fn := h.uninstall
			ops.uninstallWithOptions = func(ctx context.Context, host, inst string, o service.KitUninstallOptions, dryRun bool) (*service.KitRemovalPlan, error) {
				return fn(ctx, host, inst, o.Force, dryRun)
			}
		}
	}
	return ops
}

func (a *App) kitOf(server, instance string) *agentKitState {
	key := server + "/" + instance
	if a.agentKits == nil {
		a.agentKits = map[string]*agentKitState{}
	}
	st := a.agentKits[key]
	if st == nil {
		st = &agentKitState{}
		a.agentKits[key] = st
	}
	if !st.started {
		st.started = true
		a.readAgentKit(server, instance)
	}
	return st
}

func (a *App) readAgentKit(server, instance string) {
	key := server + "/" + instance
	if a.agentKits == nil {
		a.agentKits = map[string]*agentKitState{}
	}
	st := a.agentKits[key]
	if st == nil {
		st = &agentKitState{started: true}
		a.agentKits[key] = st
	}
	if st.read.busy {
		return
	}
	ops := a.kitOps()
	var status *agentkit.KitStatusResult
	var guide *service.KitGuide
	a.start(&st.read, time.Minute, func(ctx context.Context) error {
		var err error
		status, err = ops.status(ctx, server, instance)
		if err != nil {
			return err
		}
		guide, err = ops.guide(ctx, server, instance)
		return err
	}, func(err error) {
		st.err = err
		if err == nil {
			st.status = status
			st.guide = guide
		}
	})
}

func (a *App) agentKitCard(c *ui.Context, row *agentRow) {
	key := row.Server + "/" + row.Instance
	st := a.kitOf(row.Server, row.Instance)
	var ps *protState
	if !row.Retired() {
		ps = a.protOf(row)
	}
	card(ui.Column(c)).Gap(8).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			sectionTitle(ui.Text(c, T("kit.title"))).Grow(1)
			if st.read.busy {
				ui.Spinner(c).Size(14, 14)
			}
			ui.Button(c, T("kit.refresh")).Disabled(st.read.busy || st.actJ.busy).OnClick(func() {
				a.readAgentKit(row.Server, row.Instance)
			})
		})
		muted(ui.Text(c, T("kit.what"))).FontSize(fsSub).Wrap()
		if ps != nil && ps.view != nil && ps.view.ServerPlan == nil && !ps.read.busy {
			subPanel(ui.Column(c)).Gap(8).Background(palette.AlertBg).Border(1, palette.AlertBorder).Children(func() {
				ui.Text(c, T("kit.no_plan_warn")).FontSize(fsSub).TextColor(palette.Warn).Wrap()
				if ps.changeJ.busy {
					a.jobLine(c, &ps.changeJ)
				} else {
					if ps.changeJ.err != nil {
						errorBox(c, ps.changeJ.err, a.fold("kit-plan-err-"+key))
					}
					if ps.ref != nil {
						ui.Row(c).Children(func() {
							ui.Button(c, T("kit.enable_default_plan")).OnClick(func() {
								a.applyDesired(row, ps, defaultProtectionDesired(false))
							})
						})
					}
				}
			})
		}
		if st.err != nil {
			errorBox(c, st.err, a.fold("kit-err-"+key))
		}
		if st.status != nil {
			a.kitStatusSection(c, st)
		}
		if st.actJ.busy {
			a.jobLine(c, &st.actJ)
		}
		if st.actJ.err != nil {
			errorBox(c, st.actJ.err, a.fold("kit-act-err-"+key))
		}
		a.kitOutcomeSection(c, st)
		if st.guide != nil {
			ui.Collapsible(c, T("kit.guide_title"), a.fold("kit-guide-"+key), func() {
				a.kitGuideSection(c, st)
			})
		}
	})
	a.kitInstallModal(c, row, st)
	a.kitUninstallModal(c, row, st)
	a.kitDiffModal(c, row, st)
}

func (a *App) kitStatusSection(c *ui.Context, st *agentKitState) {
	if st.status.LastMCP != nil {
		statusRow(c, "ok", T("kit.mcp.label"), T("kit.mcp.called", "n", st.status.MCPCalls, "when", when(*st.status.LastMCP, c.Now())))
	} else {
		statusRow(c, "neutral", T("kit.mcp.label"), T("kit.mcp.never"))
	}
	if st.status.Dir == "" {
		muted(ui.Text(c, T("kit.no_skills_dir"))).FontSize(fsSub).Wrap()
		return
	}
	mono(muted(ui.Text(c, T("kit.skills_dir", "dir", st.status.Dir)))).FontSize(fsSub).Wrap()
	var hasMissing, hasOfficial, hasLocalMod, hasBoth, hasInstalled bool
	for _, s := range st.status.Skills {
		sk := s
		kind := skillChangeKind(sk)
		switch kind {
		case agentkit.SkillChangeMissing:
			hasMissing = true
		case agentkit.SkillChangeOfficialUpdate:
			hasOfficial = true
			hasInstalled = true
		case agentkit.SkillChangeLocalModified:
			hasLocalMod = true
			hasInstalled = true
		case agentkit.SkillChangeBothChanged:
			hasBoth = true
			hasInstalled = true
		default:
			hasInstalled = true
		}
		tone, label := skillStateMeta(sk, c.Now())
		ui.Row(c.Key("ks-" + sk.Skill)).Gap(8).AlignItems(ui.Center).Children(func() {
			toneDot(c, tone)
			mono(ui.Text(c, sk.Skill)).FontSize(fsSub).Width(cur.Sizes.LabelWidth)
			muted(ui.Text(c, label)).FontSize(fsSub).Grow(1).Wrap()
			if skillHasDiff(sk) {
				quietButton(c, T("kit.diff.open")).OnClick(func() { st.diffSkill = sk.Skill })
			}
		})
	}
	if st.actJ.busy {
		return
	}
	openInstall := func() {
		st.wireMCP = st.guide != nil && len(st.guide.MCPCommand) > 0
		st.restartSvc = st.guide != nil && st.guide.HasService
		st.installOpen = true
	}
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		switch {
		case hasMissing:
			ui.Button(c, T("kit.install_open")).OnClick(openInstall)
		case hasBoth:
			ui.Button(c, T("kit.compare_update_open")).OnClick(openInstall)
		case hasLocalMod:
			ui.Button(c, T("kit.reinstall_official_open")).OnClick(openInstall)
		case hasOfficial:
			ui.Button(c, T("kit.upgrade_open")).OnClick(openInstall)
		default:
			ui.Button(c, T("kit.reinstall_open")).OnClick(openInstall)
		}
		if hasInstalled {
			quietButton(c, T("kit.uninstall_open")).OnClick(func() {
				st.uninstallForce = false
				st.wireMCP = st.guide != nil && len(st.guide.MCPRemoveCmd) > 0
				st.restartSvc = st.guide != nil && st.guide.HasService
				st.uninstallOpen = true
			})
		}
	})
}

func (a *App) kitGuideSection(c *ui.Context, st *agentKitState) {
	g := st.guide
	ui.Column(c).Gap(8).Children(func() {
		if len(g.MCPCommand) > 0 {
			cmd := strings.Join(g.MCPCommand, " ")
			muted(ui.Text(c, T("kit.mcp_cmd_label"))).FontSize(fsSub).Wrap()
			a.copyableCode(c, st, cmd, T("kit.mcp_cmd_label"), T("kit.copy_mcp_cmd"))
		}
		if g.MCPSnippet != "" {
			muted(ui.Text(c, T("kit.mcp_snippet_label"))).FontSize(fsSub).Wrap()
			a.copyableCode(c, st, g.MCPSnippet, T("kit.mcp_snippet_label"), T("kit.copy_mcp_snippet"))
		}
		if g.ServerCmd != "" {
			muted(ui.Text(c, T("kit.mcp_server_cmd_label"))).FontSize(fsSub).Wrap()
			a.copyableCode(c, st, g.ServerCmd, T("kit.mcp_server_cmd_label"), T("kit.copy_server_cmd"))
		}
		if g.MCPDoc != "" {
			muted(ui.Text(c, g.MCPDoc)).FontSize(fsSub).Wrap()
		}
		ui.Text(c, T("kit.prompts_title")).FontSize(fsSub).Bold()
		prompts := []struct{ label, copyLabel, text string }{
			{T("kit.prompt.onboard"), T("kit.prompt.onboard_copy"), g.OnboardSay},
			{T("kit.prompt.status"), T("kit.prompt.status_copy"), g.StatusSay},
			{T("kit.prompt.backup"), T("kit.prompt.backup_copy"), g.BackupSay},
		}
		for i, pr := range prompts {
			if pr.text == "" {
				continue
			}
			lbl, copyLbl, txt := pr.label, pr.copyLabel, pr.text
			subPanel(ui.Row(c.Key("kp-" + strconv.Itoa(i)))).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Gap(2).Grow(1).Children(func() {
					muted(ui.Text(c, lbl)).FontSize(fsSub)
					ui.Text(c, txt).FontSize(fsSub).Wrap()
				})
				if a.copyText != nil {
					ui.Button(c, copyLbl).OnClick(func() { a.copyKitText(st, txt, lbl) })
				}
			})
		}
		if st.copied != "" {
			muted(ui.Text(c, st.copied)).FontSize(fsSub)
		}
	})
}

func (a *App) copyableCode(c *ui.Context, st *agentKitState, text, what, copyBtn string) {
	codeBox(ui.Row(c)).Gap(8).AlignItems(ui.Center).Children(func() {
		mono(ui.Text(c, text)).FontSize(fsSub).Grow(1).Wrap()
		if a.copyText != nil {
			ui.Button(c, copyBtn).OnClick(func() { a.copyKitText(st, text, what) })
		}
	})
}

func (a *App) copyKitText(st *agentKitState, text, what string) {
	if a.copyText == nil || a.copyText(text) != nil {
		st.copied = T("kit.copy_failed")
		return
	}
	st.copied = T("kit.copied_toast", "what", what)
}
