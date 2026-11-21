package profile

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// Unclassified is the Counts key of files no rule matches.
const Unclassified = "unclassified"

// SampleEntry is one sample file: a root and a slash-separated relative path.
type SampleEntry struct {
	Root string `json:"root"`
	Path string `json:"path"`
}

// SimulatedMatch is the classification of one sample.
type SimulatedMatch struct {
	Root            string   `json:"root"`
	Path            string   `json:"path"`
	Category        string   `json:"category"`
	Rule            string   `json:"rule,omitempty"`
	SQLite          bool     `json:"sqlite,omitempty"`
	SecretTables    []string `json:"secret_tables,omitempty"`
	SidecarOf       string   `json:"sidecar_of,omitempty"`
	ExclusiveSecret bool     `json:"exclusive_secret,omitempty"`
	InTiers         []string `json:"in_tiers,omitempty"`
}

// ClassifySimulation is the offline classification outcome.
type ClassifySimulation struct {
	Total        int              `json:"total"`
	Counts       map[string]int   `json:"counts"`
	Matches      []SimulatedMatch `json:"matches"`
	Unclassified []SampleEntry    `json:"unclassified,omitempty"`
	UnusedRules  []string         `json:"unused_rules,omitempty"`
}

// LoadSampleEntries reads samples from a directory (its regular files, root
// state) or a text file with one "<root>:<relpath>" or "<relpath>" per line.
func LoadSampleEntries(p string) ([]SampleEntry, error) {
	st, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	var out []SampleEntry
	if st.IsDir() {
		err := filepath.WalkDir(p, func(fp string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.Type().IsRegular() {
				return nil
			}
			rel, err := filepath.Rel(p, fp)
			if err != nil {
				return err
			}
			out = append(out, SampleEntry{StateRoot, filepath.ToSlash(rel)})
			return nil
		})
		return out, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		root, rel := StateRoot, line
		if r, rest, ok := strings.Cut(line, ":"); ok {
			root, rel = r, rest
		}
		rel = strings.TrimPrefix(strings.TrimPrefix(rel, "./"), "/")
		rel = strings.TrimPrefix(rel, "/")
		if root == "" || rel == "" || slices.Contains(strings.Split(rel, "/"), "..") {
			return nil, fmt.Errorf("%s 第 %d 行无效：%q", p, n, line)
		}
		out = append(out, SampleEntry{root, path.Clean(rel)})
	}
	return out, sc.Err()
}

var simTiers = map[string][]string{
	"soul": {CategoryIdentity, CategoryMemory, CategorySkills, CategoryConfig, CategorySessions, CategoryState, CategoryBodyPatch},
}

func inTier(tier, cat string) bool {
	switch tier {
	case "soul":
		return slices.Contains(simTiers["soul"], cat)
	case "soul-secrets":
		return inTier("soul", cat) || cat == CategorySecrets || cat == Unclassified
	case "full":
		return cat != CategoryBody && cat != CategoryCache && cat != CategoryIgnore
	}
	return true // raw
}

// SimulateClassify classifies samples with the same classifier the backup uses.
func SimulateClassify(p *Profile, samples []SampleEntry) ClassifySimulation {
	c := NewClassifier(p, nil)
	sim := ClassifySimulation{Total: len(samples), Counts: map[string]int{}, Matches: []SimulatedMatch{}}
	used := map[string]bool{}
	for _, s := range samples {
		m := c.Classify(s.Root, s.Path)
		cat := m.Category
		if cat == "" {
			cat = Unclassified
			sim.Unclassified = append(sim.Unclassified, s)
		}
		sim.Counts[cat]++
		if m.Rule != "" {
			used[m.Rule] = true
		}
		sm := SimulatedMatch{Root: s.Root, Path: s.Path, Category: cat, Rule: m.Rule}
		if ref, ok := c.SQLite(s.Root, s.Path); ok {
			sm.SQLite, sm.SecretTables = true, ref.SecretTables
		}
		tierCat := cat
		if db, ok := c.Sidecar(s.Root, s.Path); ok {
			sm.SidecarOf = db
			dm := c.Classify(s.Root, db)
			tierCat = dm.Category
			if tierCat == "" {
				tierCat = Unclassified
			}
		}
		for _, ex := range p.ExclusiveSecrets {
			if ex.Root == s.Root && ValidateGlob(ex.Glob) == nil && MatchGlob(ex.Glob, s.Path) {
				sm.ExclusiveSecret = true
			}
		}
		for _, t := range []string{"soul", "soul-secrets", "full", "raw"} {
			if inTier(t, tierCat) {
				sm.InTiers = append(sm.InTiers, t)
			}
		}
		sim.Matches = append(sim.Matches, sm)
	}
	for i, r := range p.Paths {
		if !used[fmt.Sprintf("paths[%d]", i)] {
			sim.UnusedRules = append(sim.UnusedRules, fmt.Sprintf("paths[%d] %s:%s (%s)", i, r.Root, r.Glob, r.Category))
		}
	}
	return sim
}
