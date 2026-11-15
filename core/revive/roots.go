package revive

import (
	"errors"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var ErrRootLayout = errors.New("restore root layout is invalid or ambiguous")
var rootName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// CanonicalRoots maps each logical root directly to its outermost physical
// root. A three-level layout never depends on map iteration order or a chain
// of intermediate mounts/swaps. It performs no filesystem access.
func CanonicalRoots(paths map[string]string) ([]RootPlan, error) {
	if len(paths) == 0 {
		return nil, ErrRootLayout
	}
	ids := make([]string, 0, len(paths))
	seen := map[string]bool{}
	for id, p := range paths {
		if !rootName.MatchString(id) || !filepath.IsAbs(p) || filepath.Clean(p) != p || p == string(filepath.Separator) || strings.ContainsAny(p, "\x00\r\n") || seen[p] {
			return nil, ErrRootLayout
		}
		seen[p] = true
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if (ids[i] == "state") != (ids[j] == "state") {
			return ids[i] == "state"
		}
		return ids[i] < ids[j]
	})
	out := make([]RootPlan, 0, len(ids))
	for _, id := range ids {
		r := RootPlan{ID: id, Path: paths[id]}
		for _, parent := range ids {
			p := paths[parent]
			if parent != id && strings.HasPrefix(r.Path, p+string(filepath.Separator)) && (r.SwapRoot == "" || len(p) < len(paths[r.SwapRoot])) {
				r.SwapRoot = parent
				r.Rel = strings.TrimPrefix(r.Path, p+string(filepath.Separator))
			}
		}
		out = append(out, r)
	}
	return out, nil
}
