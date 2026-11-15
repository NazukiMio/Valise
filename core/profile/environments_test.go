package profile

import (
	"strings"
	"testing"
)

const environmentYAML = `
environments:
  engine: linux-bwrap
  start_argv: ["${HOME}/program/bin/example", serve]
  working_dir: "${STATE_DIR}"
  path: ["${HOME}/program/bin"]
  program_paths: [{root: state, glob: program}]
  gateway_entry: main
  verification: {status: tested, versions: ["1.2.3"], env: "fixture Linux", date: "2026-11-12"}
`

func TestEnvironmentSchemaRequiresIndependentVerification(t *testing.T) {
	d := mustParse(t, "fixture", baseYAML+environmentYAML)
	if issues := Lint([]*Document{d})[0].Issues; len(issues) != 0 {
		t.Fatal(issues)
	}
	p := d.Profile
	if !p.EnvironmentTested("1.2.3") || p.EnvironmentTested("1.2.4") || p.ForAgent("named").Environments != nil {
		t.Fatal("verification leaked across versions/instances")
	}
	p.Environments = nil
	p.Status = StatusVerified
	if p.EnvironmentTested("1.2.3") {
		t.Fatal("legacy verification implied environment support")
	}
}

func TestEnvironmentSchemaRejectsUnscopedRuntime(t *testing.T) {
	for _, tc := range []struct{ old, replacement, field string }{
		{"engine: linux-bwrap", "engine: host", "environments.engine"},
		{"working_dir: \"${STATE_DIR}\"", "working_dir: /tmp", "environments.working_dir"},
		{"${HOME}/program/bin\"]", "${HOME}/../program/bin\"]", "environments.path"},
		{"${HOME}/program/bin\"]", "${env:PATH}\"]", "environments.path"},
		{"glob: program", "glob: '**'", "environments.program_paths"},
		{"gateway_entry: main", "gateway_entry: absent", "environments.gateway_entry"},
		{"date: \"2026-11-12\"", "date: yesterday", "environments.verification"},
		{"env: \"fixture Linux\"", "env: ''", "environments.verification"},
		{"serve]", "\"${env:SECRET}\"]", "environments.start_argv"},
	} {
		t.Run(tc.field+tc.replacement, func(t *testing.T) {
			issues := lintOne(t, baseYAML+strings.Replace(environmentYAML, tc.old, tc.replacement, 1))
			expectIssue(t, issues, tc.field, "")
		})
	}
}
