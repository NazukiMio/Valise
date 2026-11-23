package gui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/agentkit"
	"valise/core/identity"
	"valise/core/protection"
	"valise/ui/service"
)

func TestAgentKitCardAndModals(t *testing.T) {
	a, _ := world(t)
	a.prots["home/xiaoman"] = &protState{}
	tt := ui.NewTester(a.View, 1180, 1800)

	var copied []string
	a.SetClipboard(func(s string) error {
		copied = append(copied, s)
		return nil
	})

	lastCall := time.Now().Add(-3 * time.Minute)
	skills := []agentkit.SkillState{
		{Skill: "valise-onboard", Path: "/home/me/.openclaw/skills/valise-onboard/SKILL.md", State: agentkit.SkillModified, Backups: 1},
		{Skill: "valise-backup", Path: "/home/me/.openclaw/skills/valise-backup/SKILL.md", State: agentkit.SkillMissing},
	}
	var statusCalls, guideCalls int
	var installDry []bool
	var uninstallCalls []struct{ force, dry bool }

	a.agentKitHook = &agentKitOps{
		status: func(context.Context, string, string) (*agentkit.KitStatusResult, error) {
			statusCalls++
			return &agentkit.KitStatusResult{
				Dir:      "/home/me/.openclaw/skills",
				Skills:   append([]agentkit.SkillState{}, skills...),
				LastMCP:  &lastCall,
				MCPCalls: 3,
			}, nil
		},
		guide: func(context.Context, string, string) (*service.KitGuide, error) {
			guideCalls++
			return &service.KitGuide{
				HasSkillsDir: true,
				PorterPath:   "/home/me/.local/bin/valise-porter",
				ServerCmd:    "/home/me/.local/bin/valise-porter mcp --instance xiaoman",
				MCPCommand:   []string{"openclaw", "mcp", "add", "valise", "/home/me/.local/bin/valise-porter", "mcp", "--instance", "xiaoman"},
				MCPSnippet:   `{"mcpServers":{"valise":{}}}`,
				OnboardSay:   "用 valise-onboard 验收连接并报备你的目录布局。",
				StatusSay:    "用 valise-backup 看看你现在的 Valise 备份状态。",
				BackupSay:    "动手改之前，先用 valise-backup 帮我留个底。",
			}, nil
		},
		install: func(_ context.Context, _, _ string, dryRun bool) (*service.KitPlan, error) {
			installDry = append(installDry, dryRun)
			for i := range skills {
				skills[i].State = agentkit.SkillCurrent
			}
			return &service.KitPlan{
				Result: &agentkit.KitInstallResult{
					Dir:           "/home/me/.openclaw/skills",
					Written:       []string{skills[0].Path, skills[1].Path},
					LegacyRemoved: []string{"/home/me/.openclaw/skills/valise-enroll/SKILL.md"},
					LegacyKept:    []string{"/home/me/.openclaw/skills/valise-upkeep/SKILL.md"},
				},
			}, nil
		},
		uninstall: func(_ context.Context, _, _ string, force, dryRun bool) (*service.KitRemovalPlan, error) {
			uninstallCalls = append(uninstallCalls, struct{ force, dry bool }{force, dryRun})
			if force {
				for i := range skills {
					skills[i].State = agentkit.SkillMissing
				}
				return &service.KitRemovalPlan{
					Result:        &agentkit.KitUninstallResult{Removed: []string{skills[0].Path, skills[1].Path}},
					MCPCommand:    []string{"openclaw", "mcp", "remove", "valise"},
					MCPServerName: "valise",
				}, nil
			}
			return &service.KitRemovalPlan{
				Result:        &agentkit.KitUninstallResult{Removed: []string{skills[1].Path}, Kept: []string{skills[0].Path}},
				MCPCommand:    []string{"openclaw", "mcp", "remove", "valise"},
				MCPServerName: "valise",
			}, nil
		},
	}

	a.agentTab = tabManage
	a.router.Push("/agents/home/xiaoman")
	tt.Advance(time.Second)

	// Daily-tier load only; owner install/uninstall must not fire automatically.
	if statusCalls != 1 || guideCalls != 1 || len(installDry) != 0 || len(uninstallCalls) != 0 {
		t.Fatalf("unexpected RPC calls on open: status=%d guide=%d install=%v uninstall=%v", statusCalls, guideCalls, installDry, uninstallCalls)
	}
	hasAll(t, tt.Texts(), T("kit.title"), "valise-onboard", "valise-backup", T("kit.skill.missing"), T("kit.mcp.called", "n", 3, "when", when(lastCall, tt.Now())))

	// Preview modal shows exact paths and backup notice without calling install RPC.
	click(t, tt, T("kit.install_open"))
	hasAll(t, tt.Texts(), T("kit.install_title"), skills[0].Path, T("kit.install_backup_warn"), T("kit.wire_mcp_ask"), T("need.admin"))
	click(t, tt, T("kit.wire_mcp_ask"))
	hasAll(t, tt.Texts(), T("need.valise"))
	if len(installDry) != 0 {
		t.Fatalf("preview triggered install RPC: %v", installDry)
	}
	click(t, tt, T("common.cancel"))
	if len(installDry) != 0 {
		t.Fatalf("cancel triggered install RPC: %v", installDry)
	}

	// Confirm install: calls install(dryRun=false) once, auto-opens MCP guide, and surfaces legacy cleanup + kept note.
	click(t, tt, T("kit.install_open"))
	click(t, tt, T("kit.install_confirm"))
	if len(installDry) != 1 || installDry[0] {
		t.Fatalf("install calls = %v", installDry)
	}
	if statusCalls != 2 {
		t.Fatalf("expected status refresh after install, got %d", statusCalls)
	}
	if !*a.fold("kit-guide-home/xiaoman") {
		t.Fatal("expected MCP guide collapsible to open after install")
	}
	hasAll(t, tt.Texts(),
		T("kit.installed_note", "written", 2, "same", 0)+T("kit.legacy_cleaned", "n", 1),
		T("kit.legacy_kept", "paths", "/home/me/.openclaw/skills/valise-upkeep/SKILL.md"),
		T("kit.prompts_title"),
	)

	// Copy buttons send MCP command, snippet, and the 3 prompts to clipboard.
	click(t, tt, T("kit.copy_mcp_cmd"))
	click(t, tt, T("kit.copy_mcp_snippet"))
	click(t, tt, T("kit.prompt.onboard_copy"))
	click(t, tt, T("kit.prompt.backup_copy"))
	if len(copied) != 4 ||
		!strings.Contains(copied[0], "openclaw mcp add valise") ||
		copied[1] != `{"mcpServers":{"valise":{}}}` ||
		copied[2] != "用 valise-onboard 验收连接并报备你的目录布局。" ||
		copied[3] != "动手改之前，先用 valise-backup 帮我留个底。" {
		t.Fatalf("copied = %q", copied)
	}

	// Simulate locally edited skill, then test uninstall keep vs force.
	skills[0].State = agentkit.SkillModified
	click(t, tt, T("kit.refresh"))
	click(t, tt, T("kit.uninstall_open"))
	hasAll(t, tt.Texts(), T("kit.uninstall_title"), T("kit.uninstall_mcp_warn"), T("kit.uninstall_keep", "path", skills[0].Path))
	click(t, tt, T("kit.uninstall_confirm"))
	if len(uninstallCalls) != 1 || uninstallCalls[0].force || uninstallCalls[0].dry {
		t.Fatalf("uninstallCalls = %+v", uninstallCalls)
	}
	hasAll(t, tt.Texts(), T("kit.removed_summary", "removed", 1, "kept", 1), "openclaw mcp remove valise")

	// With force checked, modified files are included in removal.
	click(t, tt, T("kit.uninstall_open"))
	click(t, tt, T("kit.uninstall_force"))
	click(t, tt, T("kit.uninstall_confirm"))
	if len(uninstallCalls) != 2 || !uninstallCalls[1].force || uninstallCalls[1].dry {
		t.Fatalf("uninstallCalls after force = %+v", uninstallCalls)
	}
}

func TestAgentKitOneClickMCPAndNoPlanBanner(t *testing.T) {
	a, _ := world(t)
	ref := identity.AgentRef{ServerUID: "u", InstallID: "openclaw", InstanceID: "xiaoman", Generation: "g"}
	a.agents.value.Items[0].AgentRef = &ref
	a.prots["home/xiaoman"] = &protState{ref: &ref, view: &protection.View{Mode: "none"}}
	tt := ui.NewTester(a.View, 1180, 1800)

	var gotOpts []service.KitInstallOptions
	var gotUnOpts []service.KitUninstallOptions
	skillState := agentkit.SkillMissing
	a.agentKitHook = &agentKitOps{
		status: func(context.Context, string, string) (*agentkit.KitStatusResult, error) {
			return &agentkit.KitStatusResult{
				Dir:    "/home/me/.openclaw/skills",
				Skills: []agentkit.SkillState{{Skill: "valise-onboard", Path: "/home/me/.openclaw/skills/valise-onboard/SKILL.md", State: skillState}},
			}, nil
		},
		guide: func(context.Context, string, string) (*service.KitGuide, error) {
			return &service.KitGuide{
				HasSkillsDir: true,
				MCPCommand:   []string{"openclaw", "mcp", "add", "valise"},
				MCPRemoveCmd: []string{"openclaw", "mcp", "remove", "valise"},
				HasService:   true,
				ServiceUnit:  "openclaw-gateway.service",
			}, nil
		},
		installWithOptions: func(_ context.Context, _, _ string, o service.KitInstallOptions, _ bool) (*service.KitPlan, error) {
			gotOpts = append(gotOpts, o)
			skillState = agentkit.SkillCurrent
			return &service.KitPlan{
				Result:         &agentkit.KitInstallResult{Written: []string{"SKILL.md"}},
				MCPWire:        &service.KitStepOutcome{Attempted: true, OK: true, ExitCode: 0, Output: "added valise"},
				ServiceRestart: &service.KitServiceRestartOutcome{Attempted: true, OK: true, Healthy: false, Unit: "openclaw-gateway.service", ExitCode: 0},
			}, nil
		},
		uninstallWithOptions: func(_ context.Context, _, _ string, o service.KitUninstallOptions, _ bool) (*service.KitRemovalPlan, error) {
			gotUnOpts = append(gotUnOpts, o)
			skillState = agentkit.SkillMissing
			return &service.KitRemovalPlan{
				Result:         &agentkit.KitUninstallResult{Removed: []string{"SKILL.md"}},
				MCPCommand:     []string{"openclaw", "mcp", "remove", "valise"},
				MCPWire:        &service.KitStepOutcome{Attempted: true, OK: false, ExitCode: 2, Err: "命令退出码 2"},
				ServiceRestart: &service.KitServiceRestartOutcome{Attempted: true, OK: true, Healthy: true, Unit: "openclaw-gateway.service", ExitCode: 0},
			}, nil
		},
	}

	a.agentTab = tabManage
	a.router.Push("/agents/home/xiaoman")
	tt.Advance(time.Second)

	hasAll(t, tt.Texts(), T("kit.no_plan_warn"), T("kit.enable_default_plan"))

	click(t, tt, T("kit.install_open"))
	hasAll(t, tt.Texts(), T("kit.wire_mcp_ask"), "openclaw mcp add valise", T("kit.restart_svc_ask", "unit", "openclaw-gateway.service"), T("need.admin"))
	click(t, tt, T("kit.install_confirm"))
	if len(gotOpts) != 1 || !gotOpts[0].WireMCP || !gotOpts[0].RestartService {
		t.Fatalf("install options = %+v", gotOpts)
	}
	hasAll(t, tt.Texts(),
		T("kit.outcome.mcp_ok", "code", 0),
		"added valise",
		T("kit.outcome.restart_ok", "unit", "openclaw-gateway.service", "code", 0),
		T("kit.outcome.health_unverified", "unit", "openclaw-gateway.service"),
	)

	*a.fold("kit-guide-home/xiaoman") = false
	click(t, tt, T("kit.uninstall_open"))
	hasAll(t, tt.Texts(), T("kit.unwire_mcp_ask"), "openclaw mcp remove valise", T("kit.restart_svc_ask", "unit", "openclaw-gateway.service"))
	click(t, tt, T("kit.uninstall_confirm"))
	if len(gotUnOpts) != 1 || !gotUnOpts[0].UnwireMCP || !gotUnOpts[0].RestartService {
		t.Fatalf("uninstall options = %+v", gotUnOpts)
	}
	if !*a.fold("kit-guide-home/xiaoman") {
		t.Fatal("expected guide to auto-expand when MCP unwire failed")
	}
	hasAll(t, tt.Texts(),
		T("kit.outcome.mcp_fail", "code", 2, "err", "命令退出码 2"),
		T("kit.outcome.health_ok", "unit", "openclaw-gateway.service"),
	)
}
