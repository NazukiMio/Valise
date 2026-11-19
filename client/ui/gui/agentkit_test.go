package gui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/agentkit"
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
		{Skill: "valise-enroll", Path: "/home/me/.openclaw/skills/valise-enroll/SKILL.md", State: agentkit.SkillCurrent},
		{Skill: "valise-upkeep", Path: "/home/me/.openclaw/skills/valise-upkeep/SKILL.md", State: agentkit.SkillModified, Backups: 1},
		{Skill: "valise-checkup", Path: "/home/me/.openclaw/skills/valise-checkup/SKILL.md", State: agentkit.SkillMissing},
		{Skill: "valise-handoff", Path: "/home/me/.openclaw/skills/valise-handoff/SKILL.md", State: agentkit.SkillCurrent},
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
				Declaration: &agentkit.Declaration{
					ApprovalMode:   agentkit.ApprovalAsk,
					SecuritySkills: []agentkit.SecuritySkill{{Name: "sec", Purpose: "guard"}},
				},
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
				EnrollSay:    "我给你装了 Valise 保护技能，你学习一下。",
				UpkeepSay:    "按 valise-upkeep 检查并整理一下你的长期记忆和文件归类。",
				CheckupSay:   "用 valise-checkup 帮我做一次健康体检并解读结果。",
				HandoffSay:   "准备新开会话，请按 valise-handoff 写好交接。",
			}, nil
		},
		install: func(_ context.Context, _, _ string, dryRun bool) (*service.KitPlan, error) {
			installDry = append(installDry, dryRun)
			for i := range skills {
				skills[i].State = agentkit.SkillCurrent
			}
			return &service.KitPlan{
				Result: &agentkit.KitInstallResult{
					Dir:     "/home/me/.openclaw/skills",
					Written: []string{skills[1].Path, skills[2].Path},
					Same:    []string{skills[0].Path, skills[3].Path},
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
				Result:        &agentkit.KitUninstallResult{Removed: []string{skills[0].Path}, Kept: []string{skills[1].Path}},
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
	hasAll(t, tt.Texts(), T("kit.title"), "valise-upkeep", T("kit.skill.missing"), T("kit.mcp.called", "n", 3, "when", when(lastCall, tt.Now())))

	// Preview modal shows exact paths and backup notice without calling install RPC.
	click(t, tt, T("kit.install_open"))
	hasAll(t, tt.Texts(), T("kit.install_title"), skills[1].Path, T("kit.install_backup_warn"), T("need.valise"))
	if len(installDry) != 0 {
		t.Fatalf("preview triggered install RPC: %v", installDry)
	}
	click(t, tt, T("common.cancel"))
	if len(installDry) != 0 {
		t.Fatalf("cancel triggered install RPC: %v", installDry)
	}

	// Confirm install: calls install(dryRun=false) once and auto-opens MCP guide.
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
	hasAll(t, tt.Texts(), T("kit.installed_note", "written", 2, "same", 2), T("kit.prompts_title"))

	// Copy buttons send MCP command, snippet, and prompt text to clipboard.
	click(t, tt, T("kit.copy_mcp_cmd"))
	click(t, tt, T("kit.copy_mcp_snippet"))
	click(t, tt, T("kit.prompt.enroll_copy"))
	if len(copied) != 3 ||
		!strings.Contains(copied[0], "openclaw mcp add valise") ||
		copied[1] != `{"mcpServers":{"valise":{}}}` ||
		copied[2] != "我给你装了 Valise 保护技能，你学习一下。" {
		t.Fatalf("copied = %q", copied)
	}

	// Simulate locally edited skill, then test uninstall keep vs force.
	skills[1].State = agentkit.SkillModified
	click(t, tt, T("kit.refresh"))
	click(t, tt, T("kit.uninstall_open"))
	hasAll(t, tt.Texts(), T("kit.uninstall_title"), T("kit.uninstall_mcp_warn"), T("kit.uninstall_keep", "path", skills[1].Path))
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
