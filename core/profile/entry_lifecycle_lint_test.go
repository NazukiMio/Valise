package profile

import (
	"strings"
	"testing"
)

func TestEntryLifecycleCommandsAndLint(t *testing.T) {
	yamlDoc := `schema: valise-profile/1
id: demo
display_name: Demo
profile_version: 1
status: draft
capabilities:
  manage: { status: untested }
detect:
  state_dir_candidates: ["${HOME}/.demo"]
  markers_any: [config.yaml]
paths:
  - { root: state, glob: ".env", category: secrets }
provenance:
  kind: binary
  binary: { name: demo }
entries:
  - id: dash
    display_name: "Dashboard"
    kind: web
    host: 127.0.0.1
    port: 9119
    path: "/"
    prepare:
      summary: "Enable dashboard"
      check_paths: ["web/dist/index.html"]
      check_argv: [demo, config, get, dash.enabled]
      expect_output: "true"
      steps:
        - [demo, config, set, dash.enabled, "true"]
      restart_service: true
      verify: pending
    start:
      summary: "Start dashboard"
      argv: [demo, dash, --no-open]
      stop_argv: [demo, dash, stop]
      wait_seconds: 10
      verify: pending
`
	doc, err := Parse([]byte(yamlDoc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	res := Lint([]*Document{doc})
	if len(res[0].Issues) != 0 {
		t.Fatalf("unexpected lint issues: %+v", res[0].Issues)
	}
	if len(res[0].Warnings) != 0 {
		t.Fatalf("unexpected lint warnings: %+v", res[0].Warnings)
	}

	cmds := Commands(doc.Profile)
	want := map[string]string{
		"entries[0].prepare.check_argv": "daily",
		"entries[0].prepare.steps[0]":   "admin",
		"entries[0].start.argv":         "owner",
		"entries[0].start.stop_argv":    "owner",
	}
	for field, tier := range want {
		found := false
		for _, c := range cmds {
			if c.Field == field {
				found = true
				if c.Tier != tier {
					t.Errorf("%s tier = %q, want %q", field, c.Tier, tier)
				}
			}
		}
		if !found {
			t.Errorf("missing command audit entry %s in %+v", field, cmds)
		}
	}
}

func TestEntryLifecycleLintRejectsUnsafe(t *testing.T) {
	cases := []struct {
		name  string
		entry string
		want  string
	}{
		{
			name: "abs check_path",
			entry: `  - id: dash
    kind: web
    host: 127.0.0.1
    port: 9119
    path: "/"
    prepare:
      check_paths: ["/etc/passwd"]
      steps: [[demo, prep]]`,
			want: "prepare.check_paths[0]",
		},
		{
			name: "dotdot check_path",
			entry: `  - id: dash
    kind: web
    host: 127.0.0.1
    port: 9119
    path: "/"
    prepare:
      check_paths: ["../secret"]
      steps: [[demo, prep]]`,
			want: "prepare.check_paths[0]",
		},
		{
			name: "prepare shell wrapper",
			entry: `  - id: dash
    kind: web
    host: 127.0.0.1
    port: 9119
    path: "/"
    prepare:
      steps: [[sh, -c, "echo hi"]]`,
			want: "prepare.steps[0]",
		},
		{
			name: "prepare empty steps",
			entry: `  - id: dash
    kind: web
    host: 127.0.0.1
    port: 9119
    path: "/"
    prepare:
      summary: "missing steps"`,
			want: "prepare.steps",
		},
		{
			name: "start shell wrapper",
			entry: `  - id: dash
    kind: web
    host: 127.0.0.1
    port: 9119
    path: "/"
    start:
      argv: [bash, -lc, "demo dash"]`,
			want: "start.argv",
		},
		{
			name: "stop_argv shell wrapper",
			entry: `  - id: dash
    kind: web
    host: 127.0.0.1
    port: 9119
    path: "/"
    start:
      argv: [demo, dash]
      stop_argv: [sh, -c, "kill 1"]`,
			want: "start.stop_argv",
		},
		{
			name: "negative wait_seconds",
			entry: `  - id: dash
    kind: web
    host: 127.0.0.1
    port: 9119
    path: "/"
    start:
      argv: [demo, dash]
      wait_seconds: -1`,
			want: "start.wait_seconds",
		},
	}

	base := `schema: valise-profile/1
id: demo
display_name: Demo
profile_version: 1
status: draft
capabilities:
  manage: { status: untested }
detect:
  state_dir_candidates: ["${HOME}/.demo"]
  markers_any: [config.yaml]
paths:
  - { root: state, glob: ".env", category: secrets }
provenance:
  kind: binary
  binary: { name: demo }
entries:
`
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := Parse([]byte(base + tc.entry + "\n"))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			res := Lint([]*Document{doc})
			found := false
			for _, iss := range res[0].Issues {
				if strings.Contains(iss.Field, tc.want) {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("expected issue on %q, got %+v", tc.want, res[0].Issues)
			}
		})
	}
}

func TestValidateEntryArgv(t *testing.T) {
	valid := []Argv{
		{"demo", "serve"},
		{"demo", "--home", "${HOME}/cfg", "--state", "${STATE_DIR}/app"},
	}
	for _, argv := range valid {
		if err := ValidateEntryArgv(argv); err != nil {
			t.Errorf("ValidateEntryArgv(%v) unexpected error: %v", argv, err)
		}
	}
	invalid := []Argv{
		nil,
		{},
		{"", "serve"},
		{"sh", "-c", "demo serve"},
		{"bash", "-lc", "demo serve"},
		{"demo", "$(id)"},
		{"demo", "`id`"},
		{"demo", "${env:SECRET}"},
		{"demo", "${OTHER}"},
		{"demo", "${HOME"},
	}
	for _, argv := range invalid {
		if err := ValidateEntryArgv(argv); err == nil {
			t.Errorf("ValidateEntryArgv(%v) expected error, got nil", argv)
		}
	}
}
