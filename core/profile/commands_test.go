package profile

import "testing"

func TestCommands(t *testing.T) {
	if Commands(nil) != nil {
		t.Fatal("nil profile")
	}
	p := &Profile{
		Detect:    &Detect{VersionArgv: Argv{"x", "--version"}},
		NativeCLI: Argv{"x", "onboard"},
		Actions:   []Action{{ID: "a", Risk: RiskManage, Argv: Argv{"x", "go"}, Steps: []ActionStep{{ID: "s", Argv: Argv{"x", "step"}}}}},
		AgentKit:  &AgentKit{MCPAddArgv: Argv{"x", "mcp", "add"}, MCPRemoveArgv: Argv{"x", "mcp", "rm"}},
		Install: &Install{Steps: []InstallStep{
			{Kind: StepNPMGlobal, Package: "pkg", Version: "1.0.0", Integrity: "sha512-z"},
			{Kind: StepScript, URL: "https://e.invalid/dir/i.sh", SHA256: "ab", Interpreter: Argv{"bash"}, Args: []string{"--y"}},
			{Kind: StepPathAdd, Dir: "${HOME}/.local/bin"},
		}},
	}
	got := Commands(p)
	if len(got) != 9 {
		t.Fatalf("got %d: %+v", len(got), got)
	}
	if got[0].Label != "检测版本" || got[0].Tier != "daily" || got[2].Tier != "owner" || got[3].Label != "管理动作 a.s" {
		t.Errorf("unexpected: %+v", got)
	}
	if got[4].Field != "agent_kit.mcp_add_argv" || got[4].Label != "注册 Valise MCP" || got[4].Tier != "admin" {
		t.Errorf("mcp_add: %+v", got[4])
	}
	if got[5].Field != "agent_kit.mcp_remove_argv" || got[5].Label != "撤除 Valise MCP" || got[5].Tier != "admin" {
		t.Errorf("mcp_remove: %+v", got[5])
	}
	sc := got[7]
	if sc.Argv[1] != "<downloaded:i.sh>" || sc.URL == "" || sc.SHA256 != "ab" {
		t.Errorf("script: %+v", sc)
	}
	if got[8].Note != "${HOME}/.local/bin" {
		t.Errorf("path_add: %+v", got[8])
	}
}
