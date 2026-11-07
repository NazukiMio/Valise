package profile

import (
	"strings"
	"testing"
)

const transactionYAML = `actions:
  - id: approval-ask
    name: 改为执行前询问
    explain: 白名单外命令先询问
    undo: 单独恢复修改前备份
    risk: config
    output: none
    steps:
      - id: policy
        argv: [example, policy, cautious]
    transaction:
      root: state
      env: {EXAMPLE_STATE_DIR: "${STAGE_DIR}"}
      writes: [config.json, approvals.json]
    verification:
      versions: ["1.2.3"]
      env: Linux user install
      date: "2026-11-05"
approval_check:
  argv: [example, policy, status]
  each: scopes
  bypass_when: {security: full}
  ask_when: [{security: allowlist}]
  repair_action: approval-ask
  repair_expect: {security: allowlist, ask: on-miss}
`

func TestTransactionSchema(t *testing.T) {
	good := baseYAML + transactionYAML
	if got := lintOne(t, good); len(got) != 0 {
		t.Fatal(got)
	}
	for name, pair := range map[string][2]string{
		"argv-and-steps":   {"    steps:", "    argv: [example]\n    steps:"},
		"read-risk":        {"risk: config", "risk: read"},
		"terminal":         {"    output: none", "    output: none\n    terminal: true"},
		"text":             {"output: none", "output: text"},
		"no-undo":          {"undo: 单独恢复修改前备份", "undo: ''"},
		"duplicate-step":   {"    transaction:", "      - id: policy\n        argv: [example]\n    transaction:"},
		"no-steps":         {"    steps:\n      - id: policy\n        argv: [example, policy, cautious]", "    steps: []"},
		"parent-write":     {"writes: [config.json, approvals.json]", "writes: [../config.json]"},
		"glob-write":       {"writes: [config.json, approvals.json]", "writes: ['*.json']"},
		"absolute-write":   {"writes: [config.json, approvals.json]", "writes: [/config.json]"},
		"env-parent":       {"${STAGE_DIR}", "${STAGE_DIR}/../live"},
		"env-double-slash": {"${STAGE_DIR}", "${STAGE_DIR}//live"},
		"env-live":         {"${STAGE_DIR}", "/live"},
		"env-runtime":      {"EXAMPLE_STATE_DIR", "PATH"},
		"root":             {"root: state", "root: workspace"},
		"version":          {"versions: [\"1.2.3\"]", "versions: []"},
		"empty-version":    {"versions: [\"1.2.3\"]", "versions: ['']"},
		"date":             {"2026-11-05", "not-date"},
		"pending-action":   {"    risk: config", "    verify: pending\n    risk: config"},
		"pending-read":     {"  each: scopes", "  verify: pending\n  each: scopes"},
		"missing-action":   {"repair_action: approval-ask", "repair_action: absent"},
		"missing-expect":   {"repair_expect: {security: allowlist, ask: on-miss}", "repair_expect: {}"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := lintOne(t, baseYAML+strings.Replace(transactionYAML, pair[0], pair[1], 1)); len(got) == 0 {
				t.Fatal("invalid transaction accepted")
			}
		})
	}
	pending := strings.Replace(transactionYAML, "    risk: config", "    verify: pending\n    risk: config", 1)
	pending = strings.Replace(pending, "  repair_action: approval-ask\n  repair_expect: {security: allowlist, ask: on-miss}\n", "", 1)
	if got := lintOne(t, baseYAML+pending); len(got) != 0 {
		t.Fatal("unassociated pending declaration", got)
	}
	p := mustParse(t, "demo", good).Profile
	a := p.FindAction("approval-ask")
	if !a.TransactionVerified("1.2.3") || a.TransactionVerified("1.2.4") || a.TransactionVerified("") {
		t.Fatal("evidence must match exact version")
	}
}

func TestRepairMatchesEveryScope(t *testing.T) {
	c := &ApprovalCheck{Each: "scopes", RepairExpect: map[string]string{"security": "allowlist", "ask": "on-miss"}}
	for _, tt := range []struct {
		doc  string
		want bool
	}{
		{`{"scopes":[{"security":"allowlist","ask":"on-miss"}]}`, true},
		{`{"scopes":[{"security":"allowlist","ask":"always"}]}`, false},
		{`{"scopes":[{"security":"allowlist","ask":"on-miss"},{"security":"full","ask":"off"}]}`, false},
		{`{"scopes":[]}`, false}, {`{}`, false}, {`{"scopes":{}}`, false},
		{`{"scopes":[{"security":"allowlist"}]}`, false},
		{`{"scopes":[{"security":"allowlist","ask":null}]}`, false},
		{`{"scopes":[null]}`, false}, {`invalid`, false},
	} {
		if got := RepairMatches(c, []byte(tt.doc)); got != tt.want {
			t.Errorf("%s: got %v", tt.doc, got)
		}
	}
	c.RepairExpect = map[string]string{"ask": "<nil>"}
	if RepairMatches(c, []byte(`{"scopes":[{}]}`)) {
		t.Fatal("missing field matched")
	}
	c.RepairExpect = map[string]string{"ask": "map[x:y]"}
	if RepairMatches(c, []byte(`{"scopes":[{"ask":{"x":"y"}}]}`)) {
		t.Fatal("structured value matched scalar expectation")
	}
}
