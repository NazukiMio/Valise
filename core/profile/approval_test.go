package profile

import "testing"

// The check on OpenClaw 2026.10.9's `exec-policy show --json` (trimmed):
// security full and ask off is bypass; any scope asking is not enough when
// another scope bypasses.
func TestEvalApproval(t *testing.T) {
	c := &ApprovalCheck{Each: "effectivePolicy.scopes",
		BypassWhen: map[string]string{"security.effective": "full", "ask.effective": "off"},
		AskWhen:    []map[string]string{{"ask.effective": "always"}, {"ask.effective": "on-miss"}, {"security.effective": "deny"}, {"security.effective": "allowlist"}}}
	scope := func(sec, ask string) string {
		return `{"security":{"effective":"` + sec + `"},"ask":{"effective":"` + ask + `"}}`
	}
	doc := func(scopes ...string) []byte {
		s := `{"effectivePolicy":{"scopes":[`
		for i, x := range scopes {
			if i > 0 {
				s += ","
			}
			s += x
		}
		return []byte(s + `]}}`)
	}
	for _, tc := range []struct {
		out  []byte
		want string
	}{
		{doc(scope("full", "off")), ApprovalBypass},
		{doc(scope("full", "always")), ApprovalAsk},
		{doc(scope("allowlist", "off")), ApprovalAsk},
		{doc(scope("allowlist", "off"), scope("full", "off")), ApprovalBypass},
		{doc(scope("full", "something-new")), ApprovalUnknown},
		{doc(), ApprovalUnknown},
		{[]byte("not json"), ApprovalUnknown},
	} {
		if got, d := EvalApproval(c, tc.out); got != tc.want {
			t.Errorf("%s → %s (%s), want %s", tc.out, got, d, tc.want)
		}
	}
}
