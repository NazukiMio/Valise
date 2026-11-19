package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
