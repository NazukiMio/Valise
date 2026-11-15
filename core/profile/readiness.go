package profile

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// OfflineReadiness contains only a fixed public metadata request and bounded
// assertions. It is not a command runner, discovery script or auth interface.
type OfflineReadiness struct {
	Kind    string            `yaml:"kind" json:"kind"`
	Socket  string            `yaml:"socket" json:"socket"`
	Request string            `yaml:"request" json:"request"`
	Checks  []ReadinessField  `yaml:"checks" json:"checks"`
	Members []ReadinessMember `yaml:"members" json:"members,omitempty"`
}

type ReadinessField struct {
	Field string `yaml:"field" json:"field"`
	Kind  string `yaml:"kind" json:"kind"`
	Value string `yaml:"value" json:"value,omitempty"`
}

type ReadinessMember struct {
	Field      string           `yaml:"field" json:"field"`
	MatchField string           `yaml:"match_field" json:"match_field"`
	MatchValue string           `yaml:"match_value" json:"match_value"`
	Checks     []ReadinessField `yaml:"checks" json:"checks"`
}

// Valid checks the standalone declaration at both signed-profile and internal
// descriptor boundaries. It never runs a request or resolves host paths.
func (r *OfflineReadiness) Valid() bool {
	if r == nil {
		return false
	}
	c := &checker{}
	c.checkOfflineReadiness(r)
	return len(c.issues) == 0
}

func (c *checker) checkOfflineReadiness(r *OfflineReadiness) {
	const f = "environments.offline_readiness"
	if r.Kind != "unix-json" {
		c.add(f+".kind", "只能是 unix-json")
	}
	if validateBaseTemplate(r.Socket, VarHome, VarStateDir) != nil || strings.Contains(r.Socket, "${env:") || strings.ContainsAny(r.Socket, "*?[]\\\x00\r\n:") {
		c.add(f+".socket", "必须是私有 HOME / STATE_DIR 内的明确 socket")
	}
	var request map[string]json.RawMessage
	if len(r.Request) == 0 || len(r.Request) > 4096 || strings.ContainsAny(r.Request, "\x00\r\n") || strings.Contains(r.Request, "${") || json.Unmarshal([]byte(r.Request), &request) != nil || request == nil {
		c.add(f+".request", "必须是无变量、无换行且不超过 4 KiB 的固定 JSON 对象")
	}
	c.readinessFields(f+".checks", r.Checks)
	if len(r.Members) > 8 {
		c.add(f+".members", "最多 8 个成员检查")
	}
	for i, m := range r.Members {
		field := fmt.Sprintf("%s.members[%d]", f, i)
		if !fieldPathRe.MatchString(m.Field) || !fieldPathRe.MatchString(m.MatchField) || !readinessValue(m.MatchValue, false) || m.MatchValue == "" {
			c.add(field, "必须声明数组字段、匹配字段和明确的实例值")
		}
		c.readinessFields(field+".checks", m.Checks)
	}
}

func (c *checker) readinessFields(field string, checks []ReadinessField) {
	if len(checks) == 0 || len(checks) > 32 {
		c.add(field, "必须声明 1 至 32 个固定检查")
	}
	for i, v := range checks {
		f := fmt.Sprintf("%s[%d]", field, i)
		if !fieldPathRe.MatchString(v.Field) {
			c.add(f+".field", "必须是点分字段路径")
		}
		switch v.Kind {
		case "string_equal", "string_array_contains":
			if !readinessValue(v.Value, v.Kind == "string_equal") {
				c.add(f+".value", "含不允许的变量或字符")
			}
		case "integer_equal":
			n, err := strconv.ParseInt(v.Value, 10, 64)
			if err != nil || strconv.FormatInt(n, 10) != v.Value {
				c.add(f+".value", "必须是明确的十进制整数")
			}
		case "boolean_equal":
			if !slices.Contains([]string{"true", "false"}, v.Value) {
				c.add(f+".value", "必须是 true 或 false")
			}
		case "nonempty_string", "positive_integer", "loopback_http_origin":
			if v.Value != "" {
				c.add(f+".value", "此检查不接受 value")
			}
		default:
			c.add(f+".kind", "不是支持的固定检查")
		}
	}
}

func readinessValue(s string, revision bool) bool {
	if len(s) > 4096 || strings.ContainsAny(s, "\x00\r\n") {
		return false
	}
	allowed := []string{"${HOME}", "${STATE_DIR}"}
	if revision {
		allowed = append(allowed, "${PROGRAM_REVISION}")
	}
	for _, variable := range allowed {
		s = strings.ReplaceAll(s, variable, "")
	}
	return !strings.Contains(s, "${")
}
