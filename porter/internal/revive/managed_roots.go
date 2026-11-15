package revive

import (
	"os"
	"path/filepath"
	"strings"

	"valise/core/revive"
	"valise/porter/internal/environments"
	"valise/porter/internal/isolated"
)

// managedRootLayout rejects request-defined aliases and intermediate chains.
// It preserves the frozen plan's physical ordering for existing flat journals.
func managedRootLayout(p revive.Plan) ([]revive.RootPlan, error) {
	paths := map[string]string{}
	for _, r := range p.Roots {
		if _, duplicate := paths[r.ID]; duplicate {
			return nil, managedDataError()
		}
		paths[r.ID] = r.Path
	}
	canonical, err := revive.CanonicalRoots(paths)
	if err != nil {
		return nil, managedDataError()
	}
	want := map[string]revive.RootPlan{}
	for _, r := range canonical {
		want[r.ID] = r
	}
	var physical []revive.RootPlan
	for _, r := range p.Roots {
		if r != want[r.ID] {
			return nil, managedDataError()
		}
		if r.SwapRoot == "" {
			physical = append(physical, r)
		}
	}
	return physical, nil
}

// ManagedBindings derives only outermost host mounts. Nested logical roots
// must have the same relative positions in the authenticated guest layout.
func ManagedBindings(p revive.Plan, layout environments.Layout) ([]isolated.DataBinding, error) {
	physical, err := managedRootLayout(p)
	if err != nil || len(layout.Roots) != len(p.Roots) {
		return nil, managedDataError()
	}
	guest, err := revive.CanonicalRoots(layout.Roots)
	if err != nil {
		return nil, managedDataError()
	}
	byID := map[string]revive.RootPlan{}
	for _, r := range guest {
		byID[r.ID] = r
	}
	for _, r := range p.Roots {
		g, ok := byID[r.ID]
		if !ok || g.SwapRoot != r.SwapRoot || g.Rel != r.Rel {
			return nil, managedDataError()
		}
	}
	out := make([]isolated.DataBinding, 0, len(physical))
	for _, r := range physical {
		out = append(out, isolated.DataBinding{Host: r.Path, Guest: layout.Roots[r.ID]})
	}
	return out, nil
}

func originalLogicalRoots(j *revive.Journal) error {
	m, p := j.ManagedData, j.Plan
	physical, err := managedRootLayout(p)
	if err != nil || originalLogicalShape(j) != nil {
		return managedDataError()
	}
	if len(physical) == len(p.Roots) && len(m.LogicalRoots) == 0 {
		return nil
	} // existing flat records
	if len(m.LogicalRoots) != len(p.Roots) {
		return managedDataError()
	}
	bases := map[string]string{}
	for i, r := range physical {
		if m.OriginalRoots[i].ID != r.ID {
			return managedDataError()
		}
		locations := []string{r.Path}
		if len(j.Roots) > 0 {
			if len(j.Roots) != len(physical) || j.Roots[i].ID != r.ID {
				return managedDataError()
			}
			locations = append(locations, j.Roots[i].Prev)
		}
		for _, path := range locations {
			got, e := managedRootIdentity(r.ID, path)
			if os.IsNotExist(e) {
				continue
			}
			if e != nil {
				return e
			}
			if got == m.OriginalRoots[i] {
				if bases[r.ID] != "" {
					return managedDataError()
				}
				bases[r.ID] = path
			}
		}
		if bases[r.ID] == "" {
			return managedDataError()
		}
	}
	for i, r := range p.Roots {
		id, rel := r.ID, ""
		if r.SwapRoot != "" {
			id, rel = r.SwapRoot, r.Rel
		}
		path := bases[id]
		if path == "" || m.LogicalRoots[i].ID != r.ID {
			return managedDataError()
		}
		got, err := managedLogicalIdentity(r.ID, path, rel)
		if err != nil || got != m.LogicalRoots[i] {
			return managedDataError()
		}
	}
	return nil
}

// During explicit old-environment cleanup only the candidate is readable.
// Preserve all frozen layout checks while allowing the old logical tree to
// disappear; swap and return still require originalLogicalRoots in full.
func originalLogicalShape(j *revive.Journal) error {
	physical, err := managedRootLayout(j.Plan)
	m := j.ManagedData
	if err != nil || m == nil || len(m.OriginalRoots) != len(physical) {
		return managedDataError()
	}
	for i, root := range physical {
		if m.OriginalRoots[i].ID != root.ID || m.OriginalRoots[i].Inode == 0 {
			return managedDataError()
		}
	}
	if len(physical) == len(j.Plan.Roots) && len(m.LogicalRoots) == 0 {
		return nil
	}
	if len(m.LogicalRoots) != len(j.Plan.Roots) {
		return managedDataError()
	}
	for i, root := range j.Plan.Roots {
		if m.LogicalRoots[i].ID != root.ID || m.LogicalRoots[i].Inode == 0 {
			return managedDataError()
		}
	}
	return nil
}

func managedLogicalIdentity(id, base, rel string) (revive.ManagedRoot, error) {
	path := base
	if _, err := managedRootIdentity(id, path); err != nil {
		return revive.ManagedRoot{}, err
	}
	if rel != "" {
		for _, part := range strings.Split(rel, string(filepath.Separator)) {
			path = filepath.Join(path, part)
			if _, err := managedRootIdentity(id, path); err != nil {
				return revive.ManagedRoot{}, err
			}
		}
	}
	return managedRootIdentity(id, path)
}

// New stages contain every declared logical directory even if its snapshot
// has no files. No creation is allowed in the live or retained directories.
func createLogicalStageRoots(j *revive.Journal) error {
	if j.ManagedData == nil {
		return nil
	}
	if _, err := managedRootLayout(j.Plan); err != nil {
		return err
	}
	for _, r := range j.Plan.Roots {
		if r.SwapRoot == "" {
			continue
		}
		for _, base := range j.Roots {
			if base.ID == r.SwapRoot {
				if err := mkdirNoFollow(base.Stage, filepath.Join(base.Stage, r.Rel)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
