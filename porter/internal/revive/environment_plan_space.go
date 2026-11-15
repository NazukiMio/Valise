package revive

import (
	"math"
	"os"
	"path/filepath"
	"sort"
	"syscall"

	"valise/core/environment"
	"valise/core/revive"
	"valise/porter/internal/environments"
)

func planEnvironmentSpace(home string, roots []revive.RootPlan, program environments.ProgramMaterial, data environment.DataPlan) ([]revive.EnvironmentSpace, error) {
	physical, err := managedRootLayout(revive.Plan{Roots: roots})
	if err != nil {
		return nil, err
	}
	groups := map[uint64]*revive.EnvironmentSpace{}
	group := func(path string) (*revive.EnvironmentSpace, error) {
		path = nearestExisting(path)
		st, err := os.Stat(path)
		if err != nil {
			return nil, planError("environment_plan_space")
		}
		s, ok := st.Sys().(*syscall.Stat_t)
		if !ok {
			return nil, planError("environment_plan_space")
		}
		key := uint64(s.Dev)
		if g := groups[key]; g != nil {
			return g, nil
		}
		free, err := statfsFree(path)
		if err != nil {
			return nil, planError("environment_plan_space")
		}
		g := &revive.EnvironmentSpace{FreeBytes: free, NeedBytes: 1 << 20}
		groups[key] = g
		return g, nil
	}
	add := func(g *revive.EnvironmentSpace, size int64, copies uint64, overhead uint64) error {
		if size < 0 || uint64(size) > (math.MaxUint64-overhead)/copies {
			return planError("environment_plan_space")
		}
		n := uint64(size)*copies + overhead
		if g.NeedBytes > math.MaxUint64-n {
			return planError("environment_plan_space")
		}
		g.NeedBytes += n
		if g.NeedBytes > math.MaxInt64 {
			return planError("environment_plan_space")
		}
		return nil
	}
	programGroup, err := group(filepath.Join(home, ".local/state/valise"))
	if err != nil {
		return nil, err
	}
	programGroup.Programmes = true
	for _, f := range program.Files {
		// Three independent programme trees and one tar, including bounded
		// path/link metadata and allocation padding. The live original exists.
		padding := uint64(len(f.Path)+len(f.LinkTarget)+4096)*4 + 32768
		if err := add(programGroup, f.Size, 4, padding); err != nil {
			return nil, err
		}
	}
	rootGroups := map[string]*revive.EnvironmentSpace{}
	for _, r := range physical {
		g, err := group(filepath.Dir(r.Path))
		if err != nil {
			return nil, err
		}
		g.Roots = append(g.Roots, r.ID)
		rootGroups[r.ID] = g
	}
	for _, e := range data.Entries {
		root := e.Root
		for _, r := range roots {
			if r.ID == root && r.SwapRoot != "" {
				root = r.SwapRoot
				break
			}
		}
		g := rootGroups[root]
		if g == nil {
			return nil, planError("environment_plan_space")
		}
		var size int64
		if e.File != nil {
			size = e.File.Size
		}
		// The formal sibling stage uses its own filesystem. Candidate and
		// independent probe data reside with the private programme workspaces.
		if err := add(g, size, 1, 16384); err != nil {
			return nil, err
		}
		if err := add(programGroup, size, 2, 32768); err != nil {
			return nil, err
		}
	}
	keys := make([]uint64, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	out := make([]revive.EnvironmentSpace, 0, len(keys))
	for _, key := range keys {
		g := groups[key]
		sort.Strings(g.Roots)
		out = append(out, *g)
	}
	return out, nil
}
