package profile

import "testing"

func TestClassifyPrecedence(t *testing.T) {
	p := &Profile{
		Roots: []Root{{ID: "workspace"}},
		Paths: []PathRule{
			{Root: "state", Glob: "**", Category: CategoryMemory},
			{Root: "state", Glob: "a/key.json", Category: CategorySecrets},
			{Root: "state", Glob: "db/*.sqlite", Category: CategoryState},
		},
		Body:        &Body{Paths: []PathRef{{Root: "state", Glob: "body/**"}}},
		Consistency: &Consistency{SQLite: []SQLiteRef{{Root: "state", Glob: "db/*.sqlite", SecretTables: []string{"t"}}}},
	}
	c := NewClassifier(p, []Override{
		{Root: "state", Glob: "a/**", Category: CategoryConfig},
		{Root: "state", Glob: "body/env", Category: CategorySecrets},
		{Root: "state", Glob: "notes/**", Category: CategorySkills},
	})
	cases := map[string]string{
		"a/key.json":   CategorySecrets, // a profile secret beats an override
		"a/other.json": CategoryConfig,  // overrides beat body and paths
		"body/env":     CategorySecrets, // an override may add a secret, even inside the body
		"body/x.js":    CategoryBody,    // body beats path rules
		"notes/n.md":   CategorySkills,
		"x/y.md":       CategoryMemory, // first matching path rule
	}
	for rel, want := range cases {
		if got := c.Classify("state", rel).Category; got != want {
			t.Errorf("%s: got %s want %s", rel, got, want)
		}
	}
	if m := c.Classify("workspace", "x"); m.Category != "" {
		t.Errorf("other root must be unclassified: %+v", m)
	}
	if s, ok := c.SQLite("state", "db/a.sqlite"); !ok || s.SecretTables[0] != "t" {
		t.Error("sqlite declaration not found")
	}
	for _, rel := range []string{"db/a.sqlite-wal", "db/a.sqlite-shm", "db/a.sqlite-journal"} {
		if db, ok := c.Sidecar("state", rel); !ok || db != "db/a.sqlite" {
			t.Errorf("%s: sidecar not attributed", rel)
		}
	}
	if _, ok := c.Sidecar("state", "x/a.sqlite-wal"); ok {
		t.Error("sidecar of an undeclared db")
	}
}

func TestParseOverride(t *testing.T) {
	o, err := ParseOverride("state:notes/**=memory")
	if err != nil || o != (Override{"state", "notes/**", "memory"}) {
		t.Fatalf("%+v %v", o, err)
	}
	for _, bad := range []string{"notes=memory", "state:notes", ""} {
		if _, err := ParseOverride(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	p := &Profile{Roots: []Root{{ID: "workspace"}}}
	for _, o := range []Override{{"nowhere", "x", "memory"}, {"state", "../x", "memory"}, {"state", "x", "soul"}} {
		if o.Validate(p) == nil {
			t.Errorf("%+v accepted", o)
		}
	}
	if (Override{"workspace", "x/**", "workspace"}).Validate(p) != nil {
		t.Error("valid override rejected")
	}
}
