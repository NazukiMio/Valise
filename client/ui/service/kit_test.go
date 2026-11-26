package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"valise/core/agentkit"
)

const demoKit = `checkup:
  memory_files: [ { root: state, glob: "memory/a.md", warn_lines: 1 } ]
  memory_mechanism_markers: [ { root: state, glob: "memory/*.md" } ]
handoff:
  targets: [ { root: state, glob: "memory/a.md", mode: append_section, max_lines: 20 } ]
agent_kit:
  skills_dir: { root: state, path: "skills/" }
  mcp_add_argv: [demo, mcp, add, valise, --command, "${PORTER}", "--arg=${INSTANCE}"]
`

func TestKitInstallAndCheckup(t *testing.T) {
	s, home := enrollWorld(t)
	ctx := context.Background()
	p := filepath.Join(s.Dirs.Data, "profiles", "demo.yaml")
	os.WriteFile(p, []byte(demoProfile+demoKit), 0o644)
	if _, err := s.Enroll(ctx, "box", EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatal(err)
	}
	guide, err := s.KitGuide(ctx, "box", "demo")
	if err != nil {
		t.Fatal(err)
	}
	gcmd := strings.Join(guide.MCPCommand, " ")
	if !guide.HasSkillsDir || !strings.Contains(gcmd, "/.local/bin/valise-porter") || !strings.HasSuffix(gcmd, "--arg=demo") || strings.Contains(gcmd, "${") {
		t.Fatalf("unexpected guide: %+v", guide)
	}
	if !strings.Contains(guide.AgentLine, "mcp --instance demo") || guide.OnboardSay == "" || guide.StatusSay == "" || guide.BackupSay == "" {
		t.Fatalf("missing guide prompts: %+v", guide)
	}
	dry, err := s.KitInstall(ctx, "box", "demo", true)
	if err != nil || len(dry.Result.Written) != 2 {
		t.Fatalf("%+v %v", dry, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".demo/skills/valise-backup/SKILL.md")); !os.IsNotExist(err) {
		t.Fatal("dry run wrote files")
	}
	plan, err := s.KitInstall(ctx, "box", "demo", false)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(home, ".demo/skills/valise-backup/SKILL.md"))
	if err != nil || !strings.HasPrefix(string(b), "---\nname: valise-backup\n") {
		t.Fatalf("%q %v", b, err)
	}
	cmd := strings.Join(plan.MCPCommand, " ")
	if !strings.Contains(cmd, "/.local/bin/valise-porter") || !strings.HasSuffix(cmd, "--arg=demo") || strings.Contains(cmd, "${") {
		t.Fatalf("mcp command %q", cmd)
	}
	if !strings.Contains(plan.AgentLine, "mcp --instance demo") {
		t.Fatal(plan.AgentLine)
	}

	r, err := s.Checkup(ctx, "box", "demo")
	if err != nil || !r.MemoryMechanism || len(r.Findings) != 0 {
		t.Fatalf("%+v %v", r, err)
	}
	if ps, err := s.Proposals(ctx, "box", "demo"); err != nil || len(ps) != 0 {
		t.Fatalf("%v %v", ps, err)
	}

	st, err := s.KitStatus(ctx, "box", "demo")
	if err != nil || len(st.Skills) != 2 || st.Skills[0].State != "current" || st.LastMCP != nil {
		t.Fatalf("%+v %v", st, err)
	}
	os.WriteFile(filepath.Join(home, ".demo/skills/valise-onboard/SKILL.md"), []byte("edited"), 0o644)
	rm, err := s.KitUninstall(ctx, "box", "demo", false, false)
	if err != nil || len(rm.Result.Removed) != 1 || len(rm.Result.Kept) != 1 || !rm.MCPSnippet && len(rm.MCPCommand) != 0 {
		t.Fatalf("%+v %v", rm.Result, err)
	}
	if insts, err := s.Instances(ctx, "box"); err != nil || len(insts) != 1 || insts[0].InstanceID != "demo" {
		t.Fatalf("%+v %v", insts, err)
	}
}

func TestServerList(t *testing.T) {
	s, _ := enrollWorld(t)
	if added, err := s.AddServers("box", "box", "other"); err != nil || len(added) != 2 {
		t.Fatalf("%v %v", added, err)
	}
	if _, err := s.AddServers("-oProxyCommand=x"); err == nil {
		t.Fatal("option-like alias accepted")
	}
	if ok, _ := s.RemoveServer("other"); !ok {
		t.Fatal("remove failed")
	}
	if list, _ := s.Servers(); len(list) != 1 || list[0] != "box" {
		t.Fatalf("%v", list)
	}
}

func TestKitFilesShipTwoSkills(t *testing.T) {
	files, err := KitFiles()
	if err != nil || len(files) != 2 {
		t.Fatalf("%v %v", files, err)
	}
	for _, f := range files {
		if !strings.Contains(string(f.Content), "name: "+f.Skill+"\n") || !strings.Contains(string(f.Content), "description: ") {
			t.Errorf("%s: frontmatter name/description missing", f.Skill)
		}
	}
}

func TestKitOneClickWireAndRestart(t *testing.T) {
	origSettle := nativeServiceSettle
	nativeServiceSettle = 150 * time.Millisecond
	t.Cleanup(func() { nativeServiceSettle = origSettle })

	s, home := enrollWorld(t)
	ctx := context.Background()
	write := func(rel, body string, mode os.FileMode) {
		p := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	write("unit-state", "active\n", 0o644)
	write(".local/bin/systemctl", `#!/bin/sh
f="$HOME/unit-state"
case "$2" in
show)
  st=$(cat "$f")
  printf 'LoadState=loaded\nActiveState=%s\nUnitFileState=enabled\n' "$st"
  ;;
is-active) cat "$f" ;;
stop) echo inactive > "$f"; echo stop >> "$HOME/svc-log" ;;
reset-failed) ;;
start)
  echo start >> "$HOME/svc-log"
  if [ -f "$HOME/unit-broken" ]; then
    echo failed > "$f"
  else
    echo active > "$f"
  fi
  ;;
esac
`, 0o755)
	write(".local/bin/demo", `#!/bin/sh
if [ "$1" = "mcp" ]; then
  echo "$@" >> "$HOME/mcp-log"
  if [ -f "$HOME/mcp-fail" ]; then
    echo "boom stderr" >&2
    exit 7
  fi
  echo "wired ok"
  exit 0
fi
echo demo 1.0
`, 0o755)

	kitProf := demoProfile + `service:
  candidates: [ { scope: user, unit: demo.service } ]
agent_kit:
  skills_dir: { root: state, path: "skills/" }
  mcp_add_argv: [demo, mcp, add, valise, --command, "${PORTER}", "--arg=${INSTANCE}"]
  mcp_remove_argv: [demo, mcp, remove, valise, "--arg=${INSTANCE}"]
`
	if err := os.WriteFile(filepath.Join(s.Dirs.Data, "profiles", "demo.yaml"), []byte(kitProf), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enroll(ctx, "box", EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatal(err)
	}

	// Guide exposes service unit metadata.
	guide, err := s.KitGuide(ctx, "box", "demo")
	if err != nil {
		t.Fatal(err)
	}
	if !guide.HasService || guide.ServiceUnit != "demo.service" {
		t.Fatalf("expected HasService/ServiceUnit, got %+v", guide)
	}

	// Dry run reports planned command and unit without executing.
	dry, err := s.KitInstallWithOptions(ctx, "box", "demo", KitInstallOptions{WireMCP: true, RestartService: true}, true)
	if err != nil {
		t.Fatal(err)
	}
	if dry.MCPWire == nil || dry.MCPWire.Attempted || len(dry.MCPWire.Command) == 0 {
		t.Fatalf("unexpected dry MCPWire: %+v", dry.MCPWire)
	}
	if dry.ServiceRestart == nil || dry.ServiceRestart.Attempted || dry.ServiceRestart.Unit != "demo.service" {
		t.Fatalf("unexpected dry ServiceRestart: %+v", dry.ServiceRestart)
	}
	if _, err := os.Stat(filepath.Join(home, "mcp-log")); !os.IsNotExist(err) {
		t.Fatal("dry run executed mcp command")
	}

	// Real install with WireMCP + RestartService succeeds and reports healthy.
	plan, err := s.KitInstallWithOptions(ctx, "box", "demo", KitInstallOptions{WireMCP: true, RestartService: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Result == nil || len(plan.Result.Written) != 2 {
		t.Fatalf("skills not installed: %+v", plan.Result)
	}
	if plan.MCPWire == nil || !plan.MCPWire.Attempted || !plan.MCPWire.OK || plan.MCPWire.ExitCode != 0 || !strings.Contains(plan.MCPWire.Output, "wired ok") {
		t.Fatalf("unexpected MCPWire: %+v", plan.MCPWire)
	}
	if plan.ServiceRestart == nil || !plan.ServiceRestart.Attempted || !plan.ServiceRestart.OK || !plan.ServiceRestart.Healthy || plan.ServiceRestart.Unit != "demo.service" {
		t.Fatalf("unexpected ServiceRestart: %+v", plan.ServiceRestart)
	}
	mcpLog, _ := os.ReadFile(filepath.Join(home, "mcp-log"))
	if !strings.Contains(string(mcpLog), "mcp add valise") || !strings.Contains(string(mcpLog), "--arg=demo") {
		t.Fatalf("unexpected mcp-log: %q", string(mcpLog))
	}
	svcLog, _ := os.ReadFile(filepath.Join(home, "svc-log"))
	if !strings.Contains(string(svcLog), "stop\nstart") {
		t.Fatalf("expected stop+start in svc-log: %q", string(svcLog))
	}

	// Failure in MCP command does not discard skills result; broken service keeps OK vs Healthy separate.
	write("mcp-fail", "1", 0o644)
	write("unit-broken", "1", 0o644)
	failPlan, err := s.KitInstallWithOptions(ctx, "box", "demo", KitInstallOptions{WireMCP: true, RestartService: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	if failPlan.Result == nil {
		t.Fatal("expected skills Result even when wire fails")
	}
	if failPlan.MCPWire == nil || !failPlan.MCPWire.Attempted || failPlan.MCPWire.OK || failPlan.MCPWire.ExitCode != 7 || !strings.Contains(failPlan.MCPWire.Output, "boom stderr") {
		t.Fatalf("unexpected failed MCPWire: %+v", failPlan.MCPWire)
	}
	if failPlan.ServiceRestart == nil || !failPlan.ServiceRestart.Attempted || !failPlan.ServiceRestart.OK || failPlan.ServiceRestart.Healthy {
		t.Fatalf("expected OK=true Healthy=false for broken service: %+v", failPlan.ServiceRestart)
	}

	// Uninstall with UnwireMCP + RestartService (after fixing flags).
	_ = os.Remove(filepath.Join(home, "mcp-fail"))
	_ = os.Remove(filepath.Join(home, "unit-broken"))
	rm, err := s.KitUninstallWithOptions(ctx, "box", "demo", KitUninstallOptions{UnwireMCP: true, RestartService: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	if rm.MCPWire == nil || !rm.MCPWire.Attempted || !rm.MCPWire.OK {
		t.Fatalf("unexpected unwire outcome: %+v", rm.MCPWire)
	}
	if rm.ServiceRestart == nil || !rm.ServiceRestart.Attempted || !rm.ServiceRestart.OK || !rm.ServiceRestart.Healthy {
		t.Fatalf("unexpected uninstall restart outcome: %+v", rm.ServiceRestart)
	}
	mcpLog, _ = os.ReadFile(filepath.Join(home, "mcp-log"))
	if !strings.Contains(string(mcpLog), "mcp remove valise --arg=demo") {
		t.Fatalf("expected remove in mcp-log: %q", string(mcpLog))
	}

	// RewireMCP runs MCP command without touching skills and respects restartService=false.
	_ = os.MkdirAll(filepath.Join(home, ".demo/skills/valise-onboard"), 0o755)
	if err := os.WriteFile(filepath.Join(home, ".demo/skills/valise-onboard/SKILL.md"), []byte("keep-me"), 0o644); err != nil {
		t.Fatal(err)
	}
	rw, err := s.RewireMCP(ctx, "box", "demo", false)
	if err != nil {
		t.Fatal(err)
	}
	if rw.Result != nil || rw.ServiceRestart != nil || rw.MCPWire == nil || !rw.MCPWire.OK {
		t.Fatalf("unexpected RewireMCP(false): %+v", rw)
	}
	if b, _ := os.ReadFile(filepath.Join(home, ".demo/skills/valise-onboard/SKILL.md")); string(b) != "keep-me" {
		t.Fatalf("RewireMCP touched skills: %q", b)
	}

	// Refused command is never executed.
	refused := s.kitRunStep(ctx, "box", nil, []string{"rm", "-rf", "/"}, "demo", false, "kit-test-")
	if refused.Attempted || refused.OK || refused.Err == "" {
		t.Fatalf("expected refused command to be rejected without running: %+v", refused)
	}
}

func TestFillKitSkillChange(t *testing.T) {
	res := &agentkit.KitStatusResult{Skills: []agentkit.SkillState{
		{Skill: "a", State: agentkit.SkillModified},
		{Skill: "b", State: agentkit.SkillMissing},
		{Skill: "c", State: agentkit.SkillCurrent},
		{Skill: "d", State: agentkit.SkillModified, ChangeKind: agentkit.SkillChangeBothChanged},
	}}
	fillKitSkillChange(res, []agentkit.SkillFile{{Skill: "a", Name: "SKILL.md", Content: []byte("new")}, {Skill: "d", Content: []byte("x")}})
	got := []string{res.Skills[0].ChangeKind, res.Skills[1].ChangeKind, res.Skills[2].ChangeKind, res.Skills[3].ChangeKind}
	want := []string{"local_modified", "missing", "current", "both_changed"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d: %s", i, got[i])
		}
	}
	if res.Skills[0].ShippedContent != "new" || res.Skills[2].ShippedContent != "" || res.Skills[3].ShippedContent != "x" {
		t.Errorf("%+v", res.Skills)
	}
}
