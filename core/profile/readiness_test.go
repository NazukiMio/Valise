package profile

import (
	"strings"
	"testing"
)

const offlineEnvironmentYAML = `
environments:
  engine: linux-bwrap
  start_argv: ["${STATE_DIR}/program/agent", serve]
  working_dir: "${STATE_DIR}"
  path: ["${HOME}/program/bin"]
  program_paths: [{root: state, glob: program}]
  offline_readiness:
    kind: unix-json
    socket: "${STATE_DIR}/gateway.sock"
    request: '{"protocol":1,"verb":"identify","id":1}'
    checks:
      - {field: ok, kind: boolean_equal, value: "true"}
      - {field: result.runtime_protocol, kind: integer_equal, value: "1"}
      - {field: result.state, kind: string_equal, value: ready}
      - {field: result.code_sha, kind: string_equal, value: "${PROGRAM_REVISION}"}
      - {field: result.authority_epoch, kind: positive_integer}
      - {field: result.instance_id, kind: nonempty_string}
      - {field: result.api_origin, kind: loopback_http_origin}
      - {field: result.capabilities, kind: string_array_contains, value: session-authority-v1}
    members:
      - field: result.served_profiles
        match_field: home
        match_value: "${STATE_DIR}"
        checks: [{field: profile_id, kind: nonempty_string}]
  verification: {status: untested}
`

func TestOfflineReadinessSchema(t *testing.T) {
	d := mustParse(t, "fixture", baseYAML+offlineEnvironmentYAML)
	if issues := Lint([]*Document{d})[0].Issues; len(issues) != 0 {
		t.Fatal(issues)
	}
	if !d.Profile.Environments.OfflineReadiness.Valid() || d.Profile.EnvironmentTested("1.2.3") {
		t.Fatal("declaration became tested or failed standalone validation")
	}
	for _, tc := range []struct{ old, replacement string }{
		{"kind: unix-json", "kind: command"},
		{"${STATE_DIR}/gateway.sock", "/run/session.sock"},
		{"${STATE_DIR}/gateway.sock", "${env:SOCKET}"},
		{`request: '{"protocol":1,"verb":"identify","id":1}'`, `request: '[1,2]'`},
		{`request: '{"protocol":1,"verb":"identify","id":1}'`, `request: '{"verb":"${STATE_DIR}"}'`},
		{"kind: integer_equal, value: \"1\"", "kind: integer_equal, value: \"1.0\""},
		{"kind: boolean_equal, value: \"true\"", "kind: boolean_equal, value: \"yes\""},
		{"kind: nonempty_string}", "kind: nonempty_string, value: unexpected}"},
		{"field: result.state", "field: 'result[0].state'"},
		{"value: ready", "value: '${env:KEY}'"},
		{"match_value: \"${STATE_DIR}\"", "match_value: \"${PROGRAM_REVISION}\""},
		{"kind: string_equal, value: ready", "kind: evaluate, value: ready"},
		{"offline_readiness:", "gateway_entry: main\n  offline_readiness:"},
	} {
		t.Run(tc.replacement, func(t *testing.T) {
			d := mustParse(t, "fixture", baseYAML+strings.Replace(offlineEnvironmentYAML, tc.old, tc.replacement, 1))
			if issues := Lint([]*Document{d})[0].Issues; len(issues) == 0 {
				t.Fatal("accepted unsupported offline declaration")
			}
		})
	}
}
