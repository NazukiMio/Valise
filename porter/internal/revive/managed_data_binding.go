package revive

import (
	"path/filepath"
	"slices"

	"valise/core/environment"
	"valise/core/protection"
	"valise/core/revive"
	porterenroll "valise/porter/internal/enroll"
	"valise/porter/internal/environments"
	porteridentity "valise/porter/internal/identity"
)

func checkManagedData(s Store, j *revive.Journal, phases ...environment.Phase) error {
	m, p := j.ManagedData, j.Plan
	if m == nil || p.Mode != revive.ModeRollback || p.Carry || p.Body.Action != revive.BodyKeep || len(p.Body.Dirs) != 0 ||
		p.Unit != "" || p.InstallService || p.Identity != nil || p.Record != nil || len(p.Keep) != 0 || len(p.Supersedes) != 0 ||
		p.TestOnly || p.IsolatedTest != nil || j.EnvironmentTest != nil || len(j.BodyMoves) != 0 || j.ServiceWasActive || j.NPMInstalled || j.ServiceInstalled {
		return managedDataError()
	}
	digest, err := protection.Digest(p)
	if err != nil || digest != m.PlanDigest || len(m.ManifestDigest) != 64 {
		return managedDataError()
	}
	r, err := (environments.Store{Home: s.Home}).Load(m.Agent)
	if err != nil {
		return err
	}
	o := r.Operation
	if o == nil || o.ID != m.OperationID || o.Original != m.Original || o.Candidate != m.Candidate ||
		o.Snapshot != m.Snapshot || o.Snapshot.SnapshotID != p.SnapshotID || !slices.Contains(phases, o.Phase) || p.InstanceID != m.Agent.InstanceID {
		return managedDataError()
	}
	binding, err := porteridentity.Read(s.Home, p.InstanceID)
	if err != nil || binding.Agent == nil || binding.Agent.Ref != m.Agent {
		return managedDataError()
	}
	dossier, accepted, err := porterenroll.ReadInstallation(s.Home, p.InstanceID)
	if err != nil || dossier == nil || accepted == nil || dossier.InstallID != m.Agent.InstallID {
		return managedDataError()
	}
	record := dossier.Record(p.InstanceID)
	if record == nil || len(record.Roots) == 0 || len(record.Roots) != len(p.Roots) || !slices.Equal(p.Overrides, record.LocalOverrides) {
		return managedDataError()
	}
	want, _ := protection.Digest(accepted.ForAgent(record.Name))
	got, _ := protection.Digest(p.Profile)
	if want != got {
		return managedDataError()
	}
	seen := map[string]bool{}
	physical, err := managedRootLayout(p)
	if err != nil {
		return err
	}
	for _, root := range p.Roots {
		if seen[root.ID] || record.Roots[root.ID] != root.Path {
			return managedDataError()
		}
		seen[root.ID] = true
	}
	if len(j.Roots) != 0 {
		if len(j.Roots) != len(physical) {
			return managedDataError()
		}
		for i, root := range j.Roots {
			if root.ID != physical[i].ID || root.Live != physical[i].Path || root.Stage != sibling(root.Live, "stage", p.OpID, root.ID) ||
				root.Prev != sibling(root.Live, "prev", p.OpID, root.ID) || root.Failed != sibling(root.Live, "failed", p.OpID, root.ID) {
				return managedDataError()
			}
		}
	}
	checkOriginal := originalLogicalRoots
	if o.Phase == environment.Cleaning || o.OriginalCleaned {
		checkOriginal = originalLogicalShape
	}
	if err := checkOriginal(j); err != nil {
		return err
	}
	// An uncoordinated nested/shared Agent must not be carried into either slot.
	records, err := porterenroll.ListRecords(s.Home)
	if err != nil {
		return err
	}
	for _, other := range records {
		if other.InstanceID == p.InstanceID || other.Superseded != nil {
			continue
		}
		for _, path := range other.Roots {
			for _, root := range p.Roots {
				if within(path, root.Path) || within(root.Path, path) {
					return managedDataError()
				}
			}
		}
	}
	for _, root := range p.Roots {
		if !filepath.IsAbs(root.Path) || filepath.Clean(root.Path) != root.Path || isSymlinked(root.Path) {
			return managedDataError()
		}
	}
	return nil
}
