package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"valise/core/profile"
)

func TestProfilesLintCommand(t *testing.T) {
	dir := t.TempDir()
	valid := filepath.Join(dir, "example.yaml")
	err := os.WriteFile(valid, []byte(`schema: valise-profile/1
id: example
display_name: Example Harness
profile_version: 1
status: draft
detect:
  binaries: [example]
  state_dir_candidates: ["${HOME}/.example"]
  markers_any: [config.yaml]
paths:
  - { root: state, glob: ".env", category: secrets }
  - { root: state, glob: "config.yaml", category: config }
provenance:
  kind: binary
  binary: { name: example }
`), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(dir, "example-child.yaml")
	if err := os.WriteFile(child, []byte(`schema: valise-profile/1
id: example-child
display_name: Example Child
profile_version: 1
status: draft
extends: example
when: { exists: { root: state, glob: "mem.json" } }
paths:
  - { root: state, glob: "mem.json", category: memory }
`), 0o600); err != nil {
		t.Fatal(err)
	}

	out, errOut, code := runCLI(t, dir, "", "profiles", "lint", valid, child)
	if code != 0 || !strings.Contains(out, "OK   "+valid) || !strings.Contains(out, "id=example-child") {
		t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
	}

	// Child alone without parent must fail lint and return structured JSON under --json.
	out, errOut, code = runCLI(t, dir, "", "profiles", "lint", "--json", child)
	if code == 0 {
		t.Fatalf("expected failure when parent missing; out=%q", out)
	}
	var reps []profileLintReport
	if err := json.Unmarshal([]byte(out), &reps); err != nil || len(reps) != 1 || reps[0].OK || len(reps[0].Issues) == 0 {
		t.Fatalf("unexpected json report err=%v out=%q stderr=%q", err, out, errOut)
	}

	// Unknown field such as legacy identity.path must be caught at load/lint time.
	bad := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(bad, []byte(`schema: valise-profile/1
id: bad
display_name: Bad
profile_version: 1
status: draft
detect:
  binaries: [bad]
  state_dir_candidates: ["${HOME}/.bad"]
  markers_any: [bad.json]
paths:
  - { root: state, glob: "bad.json", category: config }
provenance: { kind: binary, binary: { name: bad } }
identity:
  argv: [bad, whoami, --json]
  path: identity.name
`), 0o600); err != nil {
		t.Fatal(err)
	}
	out, errOut, code = runCLI(t, dir, "", "profiles", "lint", bad)
	if code == 0 || !strings.Contains(errOut, "FAIL") {
		t.Fatalf("expected strict YAML failure for identity.path, code=%d out=%q err=%q", code, out, errOut)
	}
}

func writeLintFixtures(t *testing.T) (dir, parent, child, samples string) {
	t.Helper()
	dir = t.TempDir()
	parent = filepath.Join(dir, "example.yaml")
	child = filepath.Join(dir, "example-child.yaml")
	samples = filepath.Join(dir, "samples.txt")
	files := map[string]string{
		parent: `schema: valise-profile/1
id: example
display_name: Example Harness
profile_version: 1
status: draft
detect:
  binaries: [example]
  version_argv: [example, --version]
  state_dir_candidates: ["${HOME}/.example"]
  markers_any: [config.yaml]
paths:
  - { root: state, glob: ".env", category: secrets }
  - { root: state, glob: "config.yaml", category: config }
provenance:
  kind: binary
  binary: { name: example }
`,
		child: `schema: valise-profile/1
id: example-child
display_name: Example Child
profile_version: 1
status: draft
extends: example
when: { exists: { root: state, glob: "mem.json" } }
paths:
  - { root: state, glob: "mem.json", category: memory }
`,
		samples: "state:.env\nstate:config.yaml\nstate:mem.json\nstate:other.bin\n",
	}
	for p, body := range files {
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return
}

func TestProfilesLintCommandsAndStrict(t *testing.T) {
	dir, parent, child, _ := writeLintFixtures(t)
	out, errOut, code := runCLI(t, dir, "", "profiles", "lint", "--commands", parent, child)
	if code != 0 || !strings.Contains(out, "CMD") || !strings.Contains(out, "example --version") {
		t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
	}
	out, _, code = runCLI(t, dir, "", "profiles", "lint", "--commands", "--json", parent, child)
	var reps []profileLintReport
	if code != 0 || json.Unmarshal([]byte(out), &reps) != nil || len(reps) != 2 {
		t.Fatalf("code=%d out=%q", code, out)
	}
	found := false
	for _, r := range reps {
		for _, c := range r.Commands {
			found = found || len(c.Argv) > 0
		}
	}
	if !found {
		t.Fatalf("no commands in report: %+v", reps)
	}
	// --strict fails exactly when warnings exist.
	_, _, plain := runCLI(t, dir, "", "profiles", "lint", "--json", parent, child)
	_, _, strict := runCLI(t, dir, "", "profiles", "lint", "--strict", "--json", parent, child)
	warned := false
	for _, r := range reps {
		warned = warned || len(r.Warnings) > 0
	}
	if plain != 0 || (strict != 0) != warned {
		t.Fatalf("plain=%d strict=%d warned=%v", plain, strict, warned)
	}
}

func TestProfilesLintClassify(t *testing.T) {
	dir, parent, child, samples := writeLintFixtures(t)
	for _, order := range [][]string{{parent, child}, {child, parent}} {
		args := append([]string{"profiles", "lint", "--json", "--classify", samples}, order...)
		out, errOut, code := runCLI(t, dir, "", args...)
		var reps []profileLintReport
		if code != 0 || json.Unmarshal([]byte(out), &reps) != nil {
			t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
		}
		var sim *profile.ClassifySimulation
		for _, r := range reps {
			if r.Classify != nil {
				sim = r.Classify
				if r.ID != "example-child" {
					t.Fatalf("simulated on %s", r.ID)
				}
			}
		}
		if sim == nil || sim.Total != 4 || sim.Counts["memory"] != 1 || sim.Counts["secrets"] != 1 {
			t.Fatalf("order %v: %+v", order, sim)
		}
	}
	out, _, code := runCLI(t, dir, "", "profiles", "lint", "--classify", samples, parent)
	if code != 0 || !strings.Contains(out, "分类模拟") {
		t.Fatalf("code=%d out=%q", code, out)
	}
	_, _, code = runCLI(t, dir, "", "profiles", "lint", "--classify", samples, parent, filepath.Join(dir, "nope.yaml"))
	if code == 0 {
		t.Fatal("missing file passed")
	}
}
