package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	dry, err := s.KitInstall(ctx, "box", "demo", true)
	if err != nil || len(dry.Result.Written) != 4 {
		t.Fatalf("%+v %v", dry, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".demo/skills/valise-upkeep/SKILL.md")); !os.IsNotExist(err) {
		t.Fatal("dry run wrote files")
	}
	plan, err := s.KitInstall(ctx, "box", "demo", false)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(home, ".demo/skills/valise-upkeep/SKILL.md"))
	if err != nil || !strings.HasPrefix(string(b), "---\nname: valise-upkeep\n") {
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
}

func TestKitFilesShipFourSkills(t *testing.T) {
	files, err := KitFiles()
	if err != nil || len(files) != 4 {
		t.Fatalf("%v %v", files, err)
	}
	for _, f := range files {
		if !strings.Contains(string(f.Content), "name: "+f.Skill+"\n") || !strings.Contains(string(f.Content), "description: ") {
			t.Errorf("%s: frontmatter name/description missing", f.Skill)
		}
	}
}
