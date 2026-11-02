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

// A bypass whose every scope still reads the harness's defaults is its
// factory setting; one scope set by the user makes it not.
func TestApprovalDefault(t *testing.T) {
	c := &ApprovalCheck{Each: "effectivePolicy.scopes",
		BypassWhen:  map[string]string{"security.effective": "full", "ask.effective": "off"},
		DefaultWhen: []map[string]string{{"security.requestedSource": "OpenClaw default (full)", "ask.requestedSource": "OpenClaw default (off)"}}}
	scope := func(src string) string {
		return `{"security":{"effective":"full","requestedSource":"` + src + ` (full)"},"ask":{"effective":"off","requestedSource":"` + src + ` (off)"}}`
	}
	asking := `{"security":{"effective":"allowlist","requestedSource":"config"},"ask":{"effective":"on-miss","requestedSource":"config"}}`
	for _, tc := range []struct {
		out  string
		want bool
	}{
		{`{"effectivePolicy":{"scopes":[` + scope("OpenClaw default") + `]}}`, true},
		{`{"effectivePolicy":{"scopes":[` + scope("OpenClaw default") + `,` + asking + `]}}`, true},
		{`{"effectivePolicy":{"scopes":[` + scope("OpenClaw default") + `,` + scope("tools.exec") + `]}}`, false},
		{`{"effectivePolicy":{"scopes":[` + asking + `]}}`, false},
	} {
		if got := ApprovalDefault(c, []byte(tc.out)); got != tc.want {
			t.Errorf("%s → %v, want %v", tc.out, got, tc.want)
		}
	}
}
