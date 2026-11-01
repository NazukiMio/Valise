package install

import "testing"

func TestSatisfies(t *testing.T) {
	for _, c := range []struct {
		v, need string
		ok      bool
	}{
		{"v22.23.3", ">=24.16.0", false}, {"v24.22.1", ">=24.16.0", true}, {"v26.1.0", ">=24.16.0", true},
		{"git version 2.47.3", "", true}, {"", ">=1.0.0", false}, {"24.16.0", ">=24.16.0", true},
	} {
		if got := Satisfies(c.v, c.need); got != c.ok {
			t.Errorf("Satisfies(%q, %q) = %v", c.v, c.need, got)
		}
	}
}

func TestParseVersion(t *testing.T) {
	if v := ParseVersion("tar (GNU tar) 1.35"); v != "1.35" || !Satisfies(v, ">=1.30.0") {
		t.Fatal(v)
	}
}
