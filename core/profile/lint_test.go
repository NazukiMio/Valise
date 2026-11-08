package profile

import (
	"path/filepath"
	"strings"
	"testing"
)

const baseYAML = `schema: valise-profile/1
id: example
display_name: Example
profile_version: 1
status: draft
harness_versions_tested: []
detect:
  binaries: [example]
  version_argv: [example, --version]
  state_dir_candidates: ["${env:EXAMPLE_HOME}", "${HOME}/.example"]
  markers_any: [config.yaml]
roots:
  - id: workspace
    discover_argv: null
    default: "${STATE_DIR}/workspace"
paths:
  - { root: state, glob: ".env", category: secrets }
  - { root: state, glob: "memories/**", category: memory, verify: pending }
  - { root: workspace, glob: "**", category: workspace }
consistency:
  sqlite: [ { root: state, glob: "*.db" }, { root: state, glob: "agent.sqlite", secret_tables: [auth_store] } ]
  quiesce: none
provenance:
  kind: npm_global
  npm: { package: example }
body: { paths: [], bootstrap_argv: [], fresh_install_doc: https://example.invalid }
relocation: { env: EXAMPLE_HOME }
service:
  candidates: [ { scope: user, unit: example.service } ]
entries:
  - { id: main, kind: web, host: 127.0.0.1, port: 8080, path: "/" }
health:
  grace_seconds: 20
  checks:
    - { kind: systemd_active }
    - { kind: tcp, entry: main }
    - { kind: http, entry: main, expect_status: [200, 401] }
    - { kind: argv, argv: [example, doctor], ok_exit: [0] }
login: []
exclusive_secrets: [ { root: state, glob: "channels/**" } ]
native:
  export_argv: [example, export, "${OUT}"]
checkup:
  sessions: { root: state, glob: "sessions/*.jsonl", format: jsonl, usage_fields: null }
  memory_files: [ { root: state, glob: "MEMORY.md", warn_lines: 800 } ]
  memory_mechanism_markers: [ { root: state, glob: "memories/*.md" } ]
  native_options_doc: null   # verify: pending
handoff:
  targets: [ { root: state, glob: "MEMORY.md", mode: append_section, max_lines: 120 } ]
  new_session_hint: null
agent_kit:
  skills_dir: { root: workspace, path: "skills/" }
  mcp_config_doc: null
`

func mustParse(t *testing.T, name, src string) *Document {
	t.Helper()
	d, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse %s: %v", name, err)
	}
	d.Path = name
	return d
}

func lintOne(t *testing.T, src string) []Issue {
	t.Helper()
	return Lint([]*Document{mustParse(t, "p.yaml", src)})[0].Issues
}

func expectIssue(t *testing.T, issues []Issue, field, msg string) {
	t.Helper()
	for _, i := range issues {
		if strings.HasPrefix(i.Field, field) && strings.Contains(i.Message, msg) {
			return
		}
	}
	t.Errorf("no issue %s ~ %q in %v", field, msg, issues)
}

func TestBaseProfileClean(t *testing.T) {
	if issues := lintOne(t, baseYAML); len(issues) > 0 {
		t.Fatalf("unexpected issues: %v", issues)
	}
}

func TestPendingCountsFieldsAndComments(t *testing.T) {
	d := mustParse(t, "p.yaml", baseYAML)
	if len(d.Pending) != 2 {
		t.Fatalf("pending = %v, want field + comment", d.Pending)
	}
}

func TestShellStringRejected(t *testing.T) {
	src := strings.Replace(baseYAML, "version_argv: [example, --version]", `version_argv: "example --version"`, 1)
	_, err := Parse([]byte(src))
	if err == nil || !strings.Contains(err.Error(), "argv 数组") {
		t.Fatalf("err = %v", err)
	}
}

func TestUnknownFieldRejected(t *testing.T) {
	src := strings.Replace(baseYAML, "display_name: Example", "display_name: Example\ndisplay_nmae: typo", 1)
	if _, err := Parse([]byte(src)); err == nil {
		t.Fatal("unknown field accepted")
	}
}

func TestLintRules(t *testing.T) {
	cases := []struct {
		name, old, new, field, msg string
	}{
		{"schema", "schema: valise-profile/1", "schema: valise-profile/2", "schema", "必须是"},
		{"id", "id: example", "id: Example_1", "id", "小写字母"},
		{"absolute glob", `glob: "memories/**"`, `glob: "/etc/**"`, "paths[1].glob", "绝对路径"},
		{"dotdot glob", `glob: "memories/**"`, `glob: "../memories/**"`, "paths[1].glob", ".."},
		{"absolute candidate", `"${HOME}/.example"`, `"/opt/example"`, "detect.state_dir_candidates[1]", "开头"},
		{"dotdot root default", `default: "${STATE_DIR}/workspace"`, `default: "${STATE_DIR}/../w"`, "roots[0].default", ".."},
		{"unknown category", "category: memory", "category: memories", "paths[1].category", "未知类别"},
		{"unknown root", "{ root: state, glob: \"memories/**\"", "{ root: nowhere, glob: \"memories/**\"", "paths[1].root", "未声明的 root"},
		{"verify value", "verify: pending", "verify: yes", "paths[1].verify", "pending"},
		{"secret also ignore", `{ root: workspace, glob: "**", category: workspace }`, `{ root: state, glob: ".env", category: ignore }`, "paths[0]", "同时出现"},
		{"secret matched by cache", `{ root: workspace, glob: "**", category: workspace }`, `{ root: state, glob: "**", category: cache }`, "paths[0]", "同时出现"},
		{"secret in body", "body: { paths: []", `body: { paths: [ { root: state, glob: ".env" } ]`, "paths[0]", "body.paths[0]"},
		{"bad quiesce", "quiesce: none", "quiesce: pause", "consistency.quiesce", "stop_service"},
		{"bad provenance", "kind: npm_global", "kind: apt", "provenance.kind", "未知来源"},
		{"missing npm package", "npm: { package: example }", "npm: { package: \"\" }", "provenance.npm.package", "包名"},
		{"health entry", "{ kind: tcp, entry: main }", "{ kind: tcp, entry: nope }", "health.checks[1].entry", "不存在的入口"},
		{"health kind", "{ kind: systemd_active }", "{ kind: ping }", "health.checks[0].kind", "未知检查"},
		{"OUT outside export", "argv: [example, doctor]", `argv: [example, "${OUT}"]`, "health.checks[3].argv[1]", "${OUT}"},
		{"unknown var", `[example, export, "${OUT}"]`, `[example, export, "${TMP}"]`, "native.export_argv[2]", "${TMP}"},
		{"handoff mode", "mode: append_section", "mode: overwrite", "handoff.targets[0].mode", "append_section"},
		{"skills path", `path: "skills/"`, `path: "../skills/"`, "agent_kit.skills_dir.path", ".."},
		{"port", "port: 8080", "port: 0", "entries[0].port", "端口"},
		{"secret table name", "secret_tables: [auth_store]", "secret_tables: [\"auth; drop\"]", "consistency.sqlite[1].secret_tables[0]", "表名"},
		{"uv package", "kind: npm_global\n  npm: { package: example }", "kind: uv_tool", "provenance.uv.package", "包名"},
		{"session format", "format: jsonl", "format: csv", "checkup.sessions.format", "jsonl"},
		{"usage field path", "usage_fields: null", "usage_fields: [\"usage tokens\"]", "checkup.sessions.usage_fields[0]", "点分"},
		{"sqlite identifier", "format: jsonl, usage_fields: null", "format: sqlite, sqlite: { table: \"m; drop\", session_column: s, time_column: t, time_unit: s }", "checkup.sessions.sqlite.table", "标识符"},
		{"sqlite time unit", "format: jsonl, usage_fields: null", "format: sqlite, sqlite: { table: m, session_column: s, time_column: t, time_unit: min }", "checkup.sessions.sqlite.time_unit", "s 或 ms"},
		{"sqlite with jsonl fields", "format: jsonl, usage_fields: null", "format: sqlite, usage_fields: [a]", "checkup.sessions", "sqlite 子项"},
		{"mcp_add vars", "mcp_config_doc: null", "mcp_config_doc: null\n  mcp_add_argv: [example, mcp, add, \"${HOME}\"]", "agent_kit.mcp_add_argv[3]", "${HOME}"},
		{"verified with pending", "status: draft\nharness_versions_tested: []", "status: verified\nharness_versions_tested: [\"1.0\"]", "status", "verify: pending"},
		{"verified without versions", "status: draft", "status: verified", "harness_versions_tested", "版本"},
		{"when on top-level", "harness_versions_tested: []", "harness_versions_tested: []\nwhen: { exists: { root: state, glob: x } }", "when", "只有子 profile"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !strings.Contains(baseYAML, c.old) {
				t.Fatalf("fixture does not contain %q", c.old)
			}
			expectIssue(t, lintOne(t, strings.Replace(baseYAML, c.old, c.new, 1)), c.field, c.msg)
		})
	}
}

const childYAML = `schema: valise-profile/1
id: example-json-store
display_name: Example JSON store
profile_version: 1
status: draft
extends: example
when: { exists: { root: state, glob: "plugin/memories.json" } }
paths:
  - { root: state, glob: "plugin/**", category: memory }
checkup:
  memory_mechanism_markers: [ { root: state, glob: "plugin/memories.json" } ]
`

func TestChildProfile(t *testing.T) {
	parent := mustParse(t, "example.yaml", baseYAML)
	child := mustParse(t, "child.yaml", childYAML)
	res := Lint([]*Document{parent, child})
	if len(res[1].Issues) > 0 {
		t.Fatalf("child issues: %v", res[1].Issues)
	}
	eff := res[1].Effective
	if len(eff.Paths) != len(parent.Profile.Paths)+1 || eff.Paths[0].Category != CategorySecrets {
		t.Fatalf("effective paths = %v", eff.Paths)
	}
	if len(eff.Checkup.MemoryMechanismMarkers) != 2 || eff.Checkup.Sessions == nil {
		t.Fatalf("effective checkup = %+v", eff.Checkup)
	}
	if eff.Provenance == nil || eff.ID != "example-json-store" {
		t.Fatalf("effective = %+v", eff)
	}
}

func TestChildRestrictions(t *testing.T) {
	parent := mustParse(t, "example.yaml", baseYAML)
	cases := []struct{ name, src, field, msg string }{
		{"no parent", childYAML, "extends", "找不到父 profile"},
		{"declares detect", childYAML + "detect: { binaries: [x], state_dir_candidates: [\"${HOME}/.x\"] }\n", "detect", "只能追加"},
		{"no when", strings.Replace(childYAML, "when: { exists: { root: state, glob: \"plugin/memories.json\" } }\n", "", 1), "when", "when.exists"},
		{"quiesce", childYAML + "consistency: { quiesce: stop_service }\n", "consistency.quiesce", "quiesce"},
		{"secret hidden by child ignore", childYAML + "  - { root: state, glob: \".env\", category: ignore }\n", "paths[0]", "同时出现"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			docs := []*Document{mustParse(t, "child.yaml", strings.Replace(c.src, "checkup:\n  memory_mechanism_markers: [ { root: state, glob: \"plugin/memories.json\" } ]\n", "", 1))}
			if c.name != "no parent" {
				docs = append([]*Document{parent}, docs...)
			}
			res := Lint(docs)
			expectIssue(t, res[len(res)-1].Issues, c.field, c.msg)
		})
	}
}

func TestChildCycle(t *testing.T) {
	a := mustParse(t, "a.yaml", strings.Replace(strings.Replace(childYAML, "id: example-json-store", "id: a", 1), "extends: example", "extends: b", 1))
	b := mustParse(t, "b.yaml", strings.Replace(strings.Replace(childYAML, "id: example-json-store", "id: b", 1), "extends: example", "extends: a", 1))
	expectIssue(t, Lint([]*Document{a, b})[0].Issues, "extends", "循环")
}

func TestDuplicateID(t *testing.T) {
	res := Lint([]*Document{mustParse(t, "a.yaml", baseYAML), mustParse(t, "b.yaml", baseYAML)})
	expectIssue(t, res[1].Issues, "id", "重复")
}

func TestRepositoryProfilesPass(t *testing.T) {
	files, err := filepath.Glob("../../profiles/*.yaml")
	if err != nil || len(files) == 0 {
		t.Fatalf("no profiles found: %v", err)
	}
	var docs []*Document
	for _, f := range files {
		d, err := LoadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		docs = append(docs, d)
	}
	for _, r := range Lint(docs) {
		if len(r.Issues) > 0 {
			t.Errorf("%s: %v", r.Doc.Path, r.Issues)
		}
		if r.Doc.Profile.Status == StatusDraft && len(r.Doc.Pending) == 0 {
			t.Errorf("%s: draft profile without any verify: pending", r.Doc.Path)
		}
	}
}

func TestLintDisplayAuthRemedies(t *testing.T) {
	base := `schema: valise-profile/1
id: x
display_name: X
profile_version: 1
status: draft
detect: { binaries: [x], state_dir_candidates: ["${HOME}/.x"] }
paths: [ { root: state, glob: "**", category: memory, verify: pending } ]
provenance: { kind: binary, binary: { name: x } }
`
	cases := map[string]string{
		"display.color": "display: { color: red }\n",
		"display.icon":  "display: { icon: \"" + strings.Repeat("x\\n", 17) + "\" }\n",
		"第 1 行":         "display: { icon: \"" + strings.Repeat("一", 17) + "\" }\n",
		"entries[0].auth": `entries: [ { id: m, kind: web, host: 127.0.0.1, port: 1, path: "/",
  auth: { url_argv: [x, url], token_argv: [x, tok] } } ]
`,
		"url_suffix": `entries: [ { id: m, kind: web, host: 127.0.0.1, port: 1, path: "/", auth: { url_suffix: "#t=${TOKEN}" } } ]
`,
		"checkup.remedies.nope": "checkup: { remedies: { nope: [a] } }\n",
		"display.logo.svg":      "display: { logo: { svg: \"<svg onload=x></svg>\", source: s, license: l } }\n",
		"display.logo.license":  "display: { logo: { svg: \"<svg></svg>\", source: s } }\n",
		"必须是 SVG":               "display: { logo: { svg: \"<png/>\", source: s, license: l } }\n",
		"外部引用":                  "display: { logo: { svg: \"<svg><image href='https://x/y.png'/></svg>\", source: s, license: l } }\n",
	}
	for want, extra := range cases {
		d, err := Parse([]byte(base + extra))
		if err != nil {
			t.Fatalf("%s: %v", want, err)
		}
		res := Lint([]*Document{d})
		found := false
		for _, i := range res[0].Issues {
			if strings.Contains(i.String(), want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: no issue in %v", want, res[0].Issues)
		}
	}
	ok := base + `display: { color: "#E8603C", icon: "┌─┐\n│◕│\n└─┘", icon_source: x }
entries: [ { id: m, kind: web, host: 127.0.0.1, port: 1, path: "/", auth: { token_argv: [x, tok], url_suffix: "#token=${TOKEN}", hint: h } } ]
checkup: { remedies: { long_session: ["send /new"] } }
`
	d, err := Parse([]byte(ok))
	if err != nil {
		t.Fatal(err)
	}
	if res := Lint([]*Document{d}); len(res[0].Issues) > 0 {
		t.Fatal(res[0].Issues)
	}
}

func TestLintInstallCapabilities(t *testing.T) {
	ok := baseYAML + `install:
  id: user-prefix
  summary: "安装 Example，不需要管理员权限"
  requires:
    - binary: git
    - binary: node
      version: ">=24.16.0"
      install: [ { os_id: [debian, ubuntu], argv: [sudo, apt-get, install, -y, nodejs] } ]
  steps:
    - { kind: script, url: "https://example.invalid/i.sh", sha256: "` + strings.Repeat("a", 64) + `", interpreter: [bash], args: [--version, "1.2.3"], runtime_downloads: "Node" }
    - { kind: npm_global, package: example, version: "1.2.3", integrity: "sha512-x" }
    - { kind: argv, argv: [example, setup] }
    - { kind: path_add, dir: "${HOME}/.example/bin", optional: true, id: path, skip_effect: "ssh 里要写完整路径" }
native_cli: [example, onboard]
native_hints: ["另开终端运行 valise open"]
actions: []
capabilities:
  install: { status: tested, versions: ["1.2.3"], env: "Debian 13 x86_64" }
  restore: { status: untested }
`
	if issues := lintOne(t, strings.Replace(ok, `  - { id: main, kind: web, host: 127.0.0.1, port: 8080, path: "/" }`,
		`  - { id: main, kind: web, host: 127.0.0.1, port: 8080, path: "/", port_change: { steps: [[example, port, "${PORT}"]], restart_service: true } }`, 1)); len(issues) > 0 {
		t.Fatal(issues)
	}
	if issues := lintOne(t, strings.Replace(ok, `dir: "${HOME}/.example/bin"`, `dir: "${HOME}/../x"`, 1)); len(issues) == 0 {
		t.Fatal("accepted .. in path_add")
	}
	bad := map[string]string{
		"install.steps[0].sha256":            strings.Replace(ok, strings.Repeat("a", 64), "abc", 1),
		"install.steps[0].url":               strings.Replace(ok, "https://example.invalid/i.sh", "http://x/i.sh", 1),
		"install.steps[0].runtime_downloads": strings.Replace(ok, `, runtime_downloads: "Node"`, "", 1),
		"install.steps[1].integrity":         strings.Replace(ok, `"sha512-x"`, `"sha1-x"`, 1),
		"install.requires[1].version":        strings.Replace(ok, `">=24.16.0"`, `"24"`, 1),
		"install.steps[2].kind":              strings.Replace(ok, "{ kind: argv, argv: [example, setup] }", "{ kind: shell, argv: [example, setup] }", 1),
		"actions":                            strings.Replace(ok, "actions: []", "actions: [ { id: x } ]", 1),
		"install.steps[3].dir":               strings.Replace(ok, `dir: "${HOME}/.example/bin"`, `dir: "/usr/local/bin"`, 1),
		"install.steps[3].skip_effect":       strings.Replace(ok, `, skip_effect: "ssh 里要写完整路径"`, "", 1),
		"install.steps[3].id":                strings.Replace(ok, "optional: true, id: path,", "optional: true,", 1),
		"install.steps[2].optional":          strings.Replace(ok, "{ kind: argv, argv: [example, setup] }", "{ kind: argv, argv: [example, setup], id: x }", 1),
		"native_hints[0]":                    strings.Replace(ok, `native_hints: ["另开终端运行 valise open"]`, `native_hints: [" "]`, 1),
		"capabilities.install":               strings.Replace(ok, `, versions: ["1.2.3"], env: "Debian 13 x86_64"`, "", 1),
		"capabilities.fly":                   strings.Replace(ok, "restore: { status: untested }", "fly: { status: untested }", 1),
		"entries[0].port_change.steps": strings.Replace(ok, `  - { id: main, kind: web, host: 127.0.0.1, port: 8080, path: "/" }`,
			`  - { id: main, kind: web, host: 127.0.0.1, port: 8080, path: "/", port_change: { steps: [[example, port, "9"]] } }`, 1),
	}
	for field, src := range bad {
		issues := lintOne(t, src)
		found := false
		for _, i := range issues {
			if strings.HasPrefix(i.Field, field) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: no issue in %v", field, issues)
		}
	}
}

func TestLintIconSizes(t *testing.T) {
	row := strings.Repeat("⣿", 24)
	ok := baseYAML + "display:\n  color: \"#112233\"\n  icon: |\n" + strings.Repeat("    "+row+"\n", 12) +
		"  icon_compact: |\n" + strings.Repeat("    "+strings.Repeat("⣿", 12)+"\n", 6)
	if issues := lintOne(t, ok); len(issues) > 0 {
		t.Fatal(issues)
	}
	for name, src := range map[string]string{
		"rows":          strings.Replace(ok, "  icon_compact", "    "+row+"\n  icon_compact", 1),
		"width":         strings.Replace(ok, row, row+"⣿", 1),
		"compact width": strings.Replace(ok, strings.Repeat("⣿", 12)+"\n", strings.Repeat("⣿", 13)+"\n", 1),
		"wide":          strings.Replace(ok, row, strings.Repeat("⣿", 22)+"龙", 1),
		"tab":           strings.Replace(ok, row, strings.Repeat("⣿", 23)+"\t", 1),
	} {
		if issues := lintOne(t, src); len(issues) == 0 {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestLintActions(t *testing.T) {
	ok := baseYAML + `actions:
  - { id: status, name: 查看状态, explain: "显示状态", risk: read, argv: [example, status], output: text }
  - { id: copy-token, name: 复制令牌, explain: "复制令牌", risk: read, argv: [example, dash, --json], output: secret, secret: { json_field: url, url_param: token } }
  - { id: first-line, name: 复制, explain: "复制", risk: read, argv: [example, token], output: secret, secret: {} }
  - { id: restart, name: 重启, explain: "重启服务", risk: manage, argv: [example, restart], output: none, terminal: true }
  - { id: rotate, name: 换令牌, explain: "换令牌", undo: "无法撤销", risk: config, argv: [example, rotate, "${HOME}/x"], output: none }
`
	if issues := lintOne(t, ok); len(issues) > 0 {
		t.Fatal(issues)
	}
	p := &Profile{Actions: []Action{{ID: "a", Risk: RiskRead, Output: OutputText}, {ID: "b", Risk: RiskRead, Output: OutputSecret}, {ID: "c", Risk: RiskManage, Output: OutputNone}}}
	if p.FindAction("a").NeedsAdmin() || !p.FindAction("b").NeedsAdmin() || !p.FindAction("c").NeedsAdmin() || p.FindAction("z") != nil {
		t.Fatal("NeedsAdmin / FindAction")
	}
	bad := map[string]string{
		"actions[0].risk":             strings.Replace(ok, "risk: read, argv: [example, status]", "risk: low, argv: [example, status]", 1),
		"actions[0].output":           strings.Replace(ok, "output: text }", "output: html }", 1),
		"actions[0].explain":          strings.Replace(ok, `explain: "显示状态"`, `explain: ""`, 1),
		"actions[1].id":               strings.Replace(ok, "id: copy-token", "id: status", 1),
		"actions[1].secret.url_param": strings.Replace(ok, "{ json_field: url, url_param: token }", "{ url_param: token }", 1),
		"actions[2].secret":           strings.Replace(ok, "output: secret, secret: {} }", "output: secret }", 1),
		"actions[3].secret":           strings.Replace(ok, "output: none, terminal: true }", "output: none, terminal: true, secret: {} }", 1),
		"actions[4].undo":             strings.Replace(ok, `undo: "无法撤销", `, "", 1),
		"actions[4].argv":             strings.Replace(ok, `"${HOME}/x"`, `"${PORT}"`, 1),
	}
	for field, src := range bad {
		found := false
		for _, i := range lintOne(t, src) {
			if strings.HasPrefix(i.Field, field) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: no issue in %v", field, lintOne(t, src))
		}
	}
	if issues := lintOne(t, strings.Replace(ok, "output: secret, secret: {} }", "output: secret, secret: {}, terminal: true }", 1)); len(issues) == 0 {
		t.Error("accepted a terminal secret action")
	}
}
