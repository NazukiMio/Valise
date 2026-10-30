package profile

import (
	"fmt"
	"strings"
)

// Override is a user decision stored in the instance record's
// local_overrides: files matching Glob under Root get Category.
type Override struct {
	Root     string `json:"root"`
	Glob     string `json:"glob"`
	Category string `json:"category"`
}

// ParseOverride parses "<root>:<glob>=<category>".
func ParseOverride(s string) (Override, error) {
	rg, cat, ok := strings.Cut(s, "=")
	root, glob, ok2 := strings.Cut(rg, ":")
	if !ok || !ok2 {
		return Override{}, fmt.Errorf("格式应为 <root>:<glob>=<类别>：%q", s)
	}
	return Override{Root: root, Glob: glob, Category: cat}, nil
}

// Validate checks an override against the profile's roots.
func (o Override) Validate(p *Profile) error {
	if !IsCategory(o.Category) {
		return fmt.Errorf("未知类别 %q", o.Category)
	}
	if err := ValidateGlob(o.Glob); err != nil {
		return err
	}
	if o.Root != StateRoot && !slicesContainsRoot(p.Roots, o.Root) {
		return fmt.Errorf("profile 中没有 root %q", o.Root)
	}
	return nil
}

func slicesContainsRoot(roots []Root, id string) bool {
	for _, r := range roots {
		if r.ID == id {
			return true
		}
	}
	return false
}

// Match is the classification of one file.
type Match struct {
	// Category is empty for unclassified files.
	Category string `json:"category,omitempty"`
	// Rule names the deciding rule, e.g. "paths[3]" or "local_overrides[0]".
	Rule string `json:"rule,omitempty"`
}

// Classifier assigns categories with the precedence from
// docs/ARCHITECTURE.md section 4: secrets rules (profile and overrides),
// then local_overrides, then body.paths, then the first matching path rule.
type Classifier struct {
	p         *Profile
	overrides []Override
}

// NewClassifier returns a classifier for an effective profile.
func NewClassifier(p *Profile, overrides []Override) *Classifier {
	return &Classifier{p: p, overrides: overrides}
}

// Classify returns the category of the file at rel (slash-separated,
// relative to root).
func (c *Classifier) Classify(root, rel string) Match {
	for i, r := range c.p.Paths {
		if r.Category == CategorySecrets && r.Root == root && MatchGlob(r.Glob, rel) {
			return Match{CategorySecrets, fmt.Sprintf("paths[%d]", i)}
		}
	}
	for i, o := range c.overrides {
		if o.Category == CategorySecrets && o.Root == root && MatchGlob(o.Glob, rel) {
			return Match{CategorySecrets, fmt.Sprintf("local_overrides[%d]", i)}
		}
	}
	for i, o := range c.overrides {
		if o.Root == root && MatchGlob(o.Glob, rel) {
			return Match{o.Category, fmt.Sprintf("local_overrides[%d]", i)}
		}
	}
	if c.p.Body != nil {
		for i, r := range c.p.Body.Paths {
			if r.Root == root && MatchGlob(r.Glob, rel) {
				return Match{CategoryBody, fmt.Sprintf("body.paths[%d]", i)}
			}
		}
	}
	for i, r := range c.p.Paths {
		if r.Root == root && MatchGlob(r.Glob, rel) {
			return Match{r.Category, fmt.Sprintf("paths[%d]", i)}
		}
	}
	return Match{}
}

// SQLite returns the consistency.sqlite declaration matching the file.
func (c *Classifier) SQLite(root, rel string) (SQLiteRef, bool) {
	if c.p.Consistency == nil {
		return SQLiteRef{}, false
	}
	for _, s := range c.p.Consistency.SQLite {
		if s.Root == root && MatchGlob(s.Glob, rel) {
			return s, true
		}
	}
	return SQLiteRef{}, false
}

// SQLiteSidecarSuffixes are SQLite's companion files.
var SQLiteSidecarSuffixes = []string{"-wal", "-shm", "-journal"}

// Sidecar reports whether rel is a companion file of a declared SQLite
// database and returns the database's path.
func (c *Classifier) Sidecar(root, rel string) (string, bool) {
	for _, suf := range SQLiteSidecarSuffixes {
		if base, ok := strings.CutSuffix(rel, suf); ok {
			if _, isDB := c.SQLite(root, base); isDB {
				return base, true
			}
		}
	}
	return "", false
}
