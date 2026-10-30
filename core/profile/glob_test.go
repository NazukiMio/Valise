package profile

import "testing"

func TestMatchGlob(t *testing.T) {
	cases := []struct {
		g, p string
		want bool
	}{
		{".env", ".env", true},
		{".env", "a/.env", false},
		{"memories/**", "memories/a.md", true},
		{"memories/**", "memories/x/y/z.md", true},
		{"memories/**", "memories", true},
		{"memories/**", "memoriesx/a", false},
		{"**/node_modules/**", "node_modules/a.js", true},
		{"**/node_modules/**", "p/q/node_modules/a/b.js", true},
		{"**/node_modules/**", "p/q/a.js", false},
		{"agents/*/agent/*.sqlite", "agents/main/agent/x.sqlite", true},
		{"agents/*/agent/*.sqlite", "agents/main/sub/agent/x.sqlite", false},
		{"**", "anything/at/all", true},
		{"*.db", "state.db", true},
		{"*.db", "a/state.db", false},
	}
	for _, c := range cases {
		if err := ValidateGlob(c.g); err != nil {
			t.Fatalf("ValidateGlob(%q): %v", c.g, err)
		}
		if got := MatchGlob(c.g, c.p); got != c.want {
			t.Errorf("MatchGlob(%q, %q) = %v, want %v", c.g, c.p, got, c.want)
		}
	}
}

func TestValidateGlobRejects(t *testing.T) {
	for _, g := range []string{"", "/etc/passwd", "../x", "a/../b", "a//b", "./a", "a**", "${HOME}/x", "[a"} {
		if ValidateGlob(g) == nil {
			t.Errorf("ValidateGlob(%q) accepted", g)
		}
	}
}

func TestValidateBaseTemplate(t *testing.T) {
	ok := []string{"${HOME}/.example", "${env:EXAMPLE_HOME}", "${HOME}"}
	for _, s := range ok {
		if err := validateBaseTemplate(s, VarHome); err != nil {
			t.Errorf("%q: %v", s, err)
		}
	}
	bad := []string{"/opt/x", "~/.x", "${HOME}/../x", "${STATE_DIR}/x", "x/${HOME}", "${HOME}x", "${env:1BAD}", "${HOME", ""}
	for _, s := range bad {
		if validateBaseTemplate(s, VarHome) == nil {
			t.Errorf("%q accepted", s)
		}
	}
	if err := validateBaseTemplate("${STATE_DIR}/workspace", VarStateDir, VarHome); err != nil {
		t.Error(err)
	}
}
