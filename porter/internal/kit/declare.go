package kit

import (
	"strings"
	"time"
	"unicode"

	"valise/core/agentkit"
	"valise/core/valise"
)

// Declare records what an agent says about its own safety
// (valise_declare, docs/AGENT_KIT.md section 3): only the declaration
// itself, at most once every MinInterval. It is shown as unverified.
func Declare(home, instance string, d agentkit.Declaration, now time.Time) (*agentkit.Declaration, error) {
	switch d.ApprovalMode {
	case agentkit.ApprovalAsk, agentkit.ApprovalBypass, agentkit.ApprovalUnknown:
	default:
		return nil, valise.New("invalid_arguments", "approval_mode 只能是 ask、bypass 或 unknown", "执行风险命令前需要用户批准填 ask，不需要填 bypass，不确定填 unknown")
	}
	if len(d.SecuritySkills) > 20 {
		return nil, valise.New("invalid_arguments", "一次最多声明 20 个安全技能", "只列安全相关的技能")
	}
	st, err := LoadState(home, instance)
	if err != nil {
		return nil, err
	}
	if st.Declaration != nil {
		if wait := MinInterval - now.Sub(st.Declaration.At); wait > 0 {
			return nil, valise.New("rate_limited", "距离上一次声明不到 10 分钟", "稍后再声明")
		}
	}
	out := &agentkit.Declaration{SecuritySkills: []agentkit.SecuritySkill{}, ApprovalMode: d.ApprovalMode, At: now.UTC()}
	for _, s := range d.SecuritySkills {
		name, purpose := clean(s.Name, 80), clean(s.Purpose, 200)
		if name == "" {
			return nil, valise.New("invalid_arguments", "安全技能的 name 不能为空", "")
		}
		out.SecuritySkills = append(out.SecuritySkills, agentkit.SecuritySkill{Name: name, Purpose: purpose})
	}
	st.Declaration = out
	return out, SaveState(home, instance, st)
}

// clean keeps one line of printable text, at most n runes.
func clean(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		s = string(r[:n])
	}
	return s
}
