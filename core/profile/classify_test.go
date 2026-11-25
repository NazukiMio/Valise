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

	cSQL := c.WithSQLite([]SQLiteRef{
		{Root: "state", Glob: "db/*.sqlite", SecretTables: []string{"extra"}},
		{Root: "state", Glob: "plugins/mem.db"},
	})
	s, ok := cSQL.SQLite("state", "db/a.sqlite")
	if !ok || len(s.SecretTables) != 2 || s.SecretTables[0] != "t" || s.SecretTables[1] != "extra" {
		t.Fatalf("union secret tables failed: %+v ok=%v", s, ok)
	}
	if _, ok := cSQL.SQLite("state", "plugins/mem.db"); !ok {
		t.Fatal("extra local_sqlite not matched")
	}
	if db, ok := cSQL.Sidecar("state", "plugins/mem.db-wal"); !ok || db != "plugins/mem.db" {
		t.Fatalf("local_sqlite sidecar = %q %v", db, ok)
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
	if (Override{"ext-notes", "a/**", "memory"}).ValidateWithRoots(p, []string{"ext-notes"}) != nil {
		t.Error("override for extra local root rejected")
	}
	if (Override{"ext-other", "a/**", "memory"}).ValidateWithRoots(p, []string{"ext-notes"}) == nil {
		t.Error("override for unknown extra root accepted")
	}
}

func TestClassifierWithDefaultRootsPrecedenceAndImmutability(t *testing.T) {
	p := &Profile{
		Roots: []Root{{ID: "workspace"}},
		Paths: []PathRule{
			{Root: "ext-notes", Glob: "secret.key", Category: CategorySecrets},
			{Root: "ext-notes", Glob: "cfg.json", Category: CategoryConfig},
		},
		Body: &Body{Paths: []PathRef{{Root: "ext-notes", Glob: "bin/**"}}},
	}
	base := NewClassifier(p, []Override{
		{Root: "ext-notes", Glob: "ignored/**", Category: CategoryIgnore},
	}).WithSQLite([]SQLiteRef{{Root: "ext-notes", Glob: "notes.db"}})

	defaults := map[string]string{"ext-notes": CategoryMemory}
	withDefaults := base.WithDefaultRoots(defaults)
	// Mutating caller's map must not affect cloned classifier, and base must stay untouched.
	defaults["ext-notes"] = CategoryWorkspace
	if got := base.Classify("ext-notes", "daily/today.md").Category; got != "" {
		t.Fatalf("base classifier mutated: got %q", got)
	}
	cases := map[string]struct{ cat, rule string }{
		"secret.key":     {CategorySecrets, "paths[0]"},
		"ignored/a.tmp":  {CategoryIgnore, "local_overrides[0]"},
		"bin/tool":       {CategoryBody, "body.paths[0]"},
		"cfg.json":       {CategoryConfig, "paths[1]"},
		"daily/today.md": {CategoryMemory, "local_roots[ext-notes]"},
	}
	for rel, want := range cases {
		got := withDefaults.Classify("ext-notes", rel)
		if got.Category != want.cat || got.Rule != want.rule {
			t.Errorf("%s: got %+v, want %+v", rel, got, want)
		}
	}
	if _, ok := withDefaults.SQLite("ext-notes", "notes.db"); !ok {
		t.Fatal("WithDefaultRoots did not preserve extraSQLite")
	}
}
