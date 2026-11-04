package profile

import (
	"strings"
	"testing"
)

func TestServiceRepairReference(t *testing.T) {
	good := strings.Replace(baseYAML, "service:\n", "service:\n  repair_action: recover\n", 1) + `actions:
  - { id: recover, name: 重启服务, explain: 重启后检查, risk: manage, argv: [example, restart], output: none }
`
	if issues := lintOne(t, good); len(issues) > 0 {
		t.Fatal(issues)
	}
	for name, src := range map[string]string{
		"missing":    strings.Replace(good, "repair_action: recover", "repair_action: absent", 1),
		"read":       strings.Replace(good, "risk: manage", "risk: read", 1),
		"terminal":   strings.Replace(good, "output: none", "output: none, terminal: true", 1),
		"secret":     strings.Replace(good, "output: none", "output: secret, secret: {}", 1),
		"no-service": strings.Replace(good, "candidates: [ { scope: user, unit: example.service } ]", "candidates: []", 1),
	} {
		t.Run(name, func(t *testing.T) { expectIssue(t, lintOne(t, src), "service.repair_action", "") })
	}
	p := mustParse(t, "demo", good).Profile
	if p.ForAgent("named").Service != nil {
		t.Fatal("unverified named service retained repair")
	}
}
