package enroll

import "testing"

func TestDiff(t *testing.T) {
	a := Record{ProfileID: "p", ProfileVersion: 1, Roots: map[string]string{"state": "/s"},
		Provenance: Provenance{Kind: "npm_global", NPM: &NPMRecord{Package: "x", Version: "1.0"}}}
	if ch := Diff(a, a); len(ch) != 0 {
		t.Fatalf("%+v", ch)
	}
	b := a
	b.ProfileVersion = 2
	b.Roots = map[string]string{"state": "/s", "workspace": "/w"}
	b.Provenance = Provenance{Kind: "npm_global", NPM: &NPMRecord{Package: "x", Version: "1.1"}}
	ch := Diff(a, b)
	if len(ch) != 3 || ch[0].Field != "profile_version" || ch[1].Field != "roots.workspace" || ch[2].To != "npm x@1.1" {
		t.Fatalf("%+v", ch)
	}
}

func TestValidInstanceID(t *testing.T) {
	for _, ok := range []string{"openclaw", "hermes-2"} {
		if ValidInstanceID(ok) != nil {
			t.Error(ok)
		}
	}
	for _, bad := range []string{"", "../x", "A", "a_b", "-a"} {
		if ValidInstanceID(bad) == nil {
			t.Error(bad)
		}
	}
}
