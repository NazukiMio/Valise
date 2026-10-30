package enroll

import (
	"fmt"
	"sort"
)

// Diff lists what changed between the previous record and a new one, for
// re-enrollment after a harness upgrade.
func Diff(prev, next Record) []Change {
	var out []Change
	add := func(field, from, to string) {
		if from != to {
			out = append(out, Change{field, from, to})
		}
	}
	add("profile_id", prev.ProfileID, next.ProfileID)
	add("profile_version", fmt.Sprint(prev.ProfileVersion), fmt.Sprint(next.ProfileVersion))
	keys := map[string]bool{}
	for k := range prev.Roots {
		keys[k] = true
	}
	for k := range next.Roots {
		keys[k] = true
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	for _, k := range sorted {
		add("roots."+k, prev.Roots[k], next.Roots[k])
	}
	add("provenance.kind", prev.Provenance.Kind, next.Provenance.Kind)
	add("provenance", provenanceSummary(prev.Provenance), provenanceSummary(next.Provenance))
	return out
}

func provenanceSummary(p Provenance) string {
	switch {
	case p.NPM != nil:
		return fmt.Sprintf("npm %s@%s", p.NPM.Package, p.NPM.Version)
	case p.Git != nil:
		dirty := ""
		if p.Git.Dirty {
			dirty = " (dirty)"
		}
		return fmt.Sprintf("git %s %s%s", p.Git.Remote, p.Git.Commit, dirty)
	case p.Tool != nil:
		return fmt.Sprintf("%s %s %s", p.Kind, p.Tool.Package, p.Tool.Version)
	case p.Binary != nil:
		return fmt.Sprintf("binary %s %s", p.Binary.Path, p.Binary.SHA256)
	}
	return ""
}
