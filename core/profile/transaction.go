package profile

import (
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"strings"
	"time"
)

type ActionStep struct {
	ID   string `yaml:"id" json:"id"`
	Argv Argv   `yaml:"argv" json:"argv"`
}
type ActionTransaction struct {
	Root   string            `yaml:"root" json:"root"`
	Env    map[string]string `yaml:"env" json:"env"`
	Writes []string          `yaml:"writes" json:"writes"`
	// Scratch are areas every run of the harness CLI rewrites (INSTALL.md
	// 5.2.1): writable like Writes, and ignored when checking that the
	// approval read left the stage unchanged.
	Scratch []string `yaml:"scratch,omitempty" json:"scratch,omitempty"`
}

// Areas are all declared write areas: Writes then Scratch.
func (t *ActionTransaction) Areas() []string {
	return append(append([]string{}, t.Writes...), t.Scratch...)
}

type ActionVerification struct {
	Versions []string `yaml:"versions" json:"versions"`
	Env      string   `yaml:"env" json:"env"`
	Date     string   `yaml:"date" json:"date"`
}

// SafeWritePath is a literal, confined relative path, never a pattern.
func SafeWritePath(s string) bool {
	return s != "" && s != "." && path.Clean(s) == s && !path.IsAbs(s) && !strings.ContainsAny(s, "\\*?[]\x00\r\n") && s != ".." && !strings.HasPrefix(s, "../")
}
func (a Action) TransactionVerified(version string) bool {
	return a.Transaction != nil && len(a.Steps) > 0 && a.Verify == "" && a.Verification != nil && slices.Contains(a.Verification.Versions, version)
}

// RepairMatches is stricter than the broad ask/bypass classification.
func RepairMatches(c *ApprovalCheck, out []byte) bool {
	if c == nil || c.Verify != "" || len(c.RepairExpect) == 0 {
		return false
	}
	var doc any
	if json.Unmarshal(out, &doc) != nil {
		return false
	}
	items := []any{doc}
	if c.Each != "" {
		var ok bool
		items, ok = field(doc, c.Each).([]any)
		if !ok || len(items) == 0 {
			return false
		}
	}
	for _, it := range items {
		for k, v := range c.RepairExpect {
			got := field(it, k)
			switch got.(type) {
			case string, bool, float64:
			default:
				return false
			}
			if fmt.Sprint(got) != v {
				return false
			}
		}
	}
	return true
}

// ValidateTransaction repeats schema checks at the execution boundary.
func ValidateTransaction(a Action, approval *ApprovalCheck) []Issue {
	c := &checker{}
	c.checkActions([]Action{a})
	c.checkApproval(approval)
	if a.Transaction == nil || len(a.Steps) == 0 || a.Verify != "" || a.Verification == nil || approval == nil || approval.Verify != "" || len(approval.RepairExpect) == 0 {
		c.add("transaction", "只能执行已核实且有精确复核目标的配置事务")
	}
	return c.issues
}

func (c *checker) checkTransaction(f string, a Action) {
	if a.Steps == nil && a.Transaction == nil {
		if a.Verification != nil || a.Verify != "" {
			c.add(f, "verification/verify 只用于事务动作")
		}
		return
	}
	if a.Argv != nil || len(a.Steps) == 0 || len(a.Steps) > 16 || a.Transaction == nil || a.Risk != RiskConfig || a.Output != OutputNone || a.Terminal {
		c.add(f, "事务必须为 1–16 个固定步骤、config、none、非交互，与 argv 互斥，并声明 transaction")
	}
	ids := map[string]bool{}
	for i, s := range a.Steps {
		k := fmt.Sprintf("%s.steps[%d]", f, i)
		if !idRe.MatchString(s.ID) || ids[s.ID] {
			c.add(k+".id", "步骤 id 无效或重复")
		}
		ids[s.ID] = true
		c.argvVars(k+".argv", s.Argv, true, VarHome)
	}
	if t := a.Transaction; t != nil {
		if t.Root != StateRoot || len(t.Env) == 0 || len(t.Writes) == 0 {
			c.add(f+".transaction", "必须声明 state、暂存环境与 writes")
		}
		for k, v := range t.Env {
			suffix := strings.TrimPrefix(v, "${STAGE_DIR}")
			if !envNameRe.MatchString(k) || !strings.HasPrefix(v, "${STAGE_DIR}") || (suffix != "" && (!strings.HasPrefix(suffix, "/") || !SafeWritePath(strings.TrimPrefix(suffix, "/")))) {
				c.add(f+".transaction.env", "只能重定向到 ${STAGE_DIR} 或其安全相对路径")
			}
			if slices.Contains([]string{"PATH", "HOME", "SHELL", "LD_PRELOAD", "BASH_ENV", "ENV"}, k) {
				c.add(f+".transaction.env", "不能覆盖运行环境变量 %s", k)
			}
		}
		for _, w := range t.Writes {
			if !SafeWritePath(w) {
				c.add(f+".transaction.writes", "必须为安全的明确相对路径")
			}
		}
		for _, w := range t.Scratch {
			if !SafeWritePath(w) {
				c.add(f+".transaction.scratch", "必须为安全的明确相对路径")
			}
			for _, x := range t.Writes {
				if x == w || strings.HasPrefix(x, w+"/") || strings.HasPrefix(w, x+"/") {
					c.add(f+".transaction.scratch", "不能与 writes 重叠：%s", w)
				}
			}
		}
	}
	if a.Verify != "" && a.Verify != "pending" {
		c.add(f+".verify", "只能是 pending")
	}
	if a.Verify == "" {
		v := a.Verification
		if v == nil || len(v.Versions) == 0 || strings.TrimSpace(v.Env) == "" {
			c.add(f+".verification", "已核实事务必须有版本、环境与日期")
		} else {
			if _, err := time.Parse("2006-01-02", v.Date); err != nil {
				c.add(f+".verification.date", "必须是日期")
			}
			seen := map[string]bool{}
			for _, version := range v.Versions {
				if strings.TrimSpace(version) == "" || strings.ContainsAny(version, "\r\n\x00") || seen[version] {
					c.add(f+".verification.versions", "版本必须非空、唯一且为完整单行版本输出")
				}
				seen[version] = true
			}
		}
	}
}
func (c *checker) checkRepair(p *Profile) {
	r := p.ApprovalCheck
	if r == nil {
		return
	}
	if r.RepairAction == "" && len(r.RepairExpect) == 0 {
		return
	}
	a := p.FindAction(r.RepairAction)
	if r.Verify != "" || len(r.RepairExpect) == 0 || a == nil || a.Transaction == nil || a.Verify != "" || a.Verification == nil {
		c.add("approval_check.repair_action", "必须关联已核实事务，并声明已核实读法与 repair_expect")
	}
	for k := range r.RepairExpect {
		if !fieldPathRe.MatchString(k) {
			c.add("approval_check.repair_expect", "必须是字段路径")
		}
	}
}
