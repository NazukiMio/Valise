package activeview

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"valise/core/enroll"
	"valise/core/environment"
	"valise/core/revive"
	"valise/porter/internal/activationrecord"
	"valise/porter/internal/environments"
)

func (r Reader) checkData(parent *activationrecord.Record, rec *enroll.Record, ref environment.Ref) error {
	j, err := activationrecord.LoadData(r.Home, parent.Plan.DataOpID)
	if err != nil {
		return err
	}
	m, p := j.ManagedData, j.Plan
	if m == nil || m.Agent != parent.Plan.Agent || m.OperationID != parent.Plan.OperationID || m.Snapshot != parent.Plan.Snapshot ||
		m.Original != parent.Plan.Original || m.Candidate != parent.Plan.Candidate || m.PlanDigest != parent.DataPlanDigest || m.ManifestDigest != parent.DataManifestDigest ||
		environments.Digest(p) != parent.DataPlanDigest || environments.Digest(j.Manifest) != parent.DataManifestDigest || !j.ManifestSaved || !j.StageVerified ||
		p.InstanceID != rec.InstanceID || p.SnapshotID != m.Snapshot.SnapshotID || p.Mode != revive.ModeRollback || p.Carry || p.TestOnly || p.IsolatedTest != nil || p.Identity != nil || p.Record != nil || p.Unit != "" ||
		p.Body.Action != revive.BodyKeep || len(p.Body.Dirs) != 0 || p.InstallService || len(p.Keep) != 0 || len(p.Supersedes) != 0 ||
		j.EnvironmentTest != nil || j.NPMInstalled || j.ServiceInstalled || j.ServiceWasActive || len(j.BodyMoves) != 0 || !slices.Equal(p.Overrides, rec.LocalOverrides) {
		return unavailable()
	}
	wantState := revive.StateRestored
	if ref == m.Original {
		if j.State != revive.StatePlanned && j.State != revive.StateRolledBack {
			return unavailable()
		}
	} else if ref != m.Candidate || j.State != wantState {
		return unavailable()
	}
	paths := map[string]string{}
	for _, root := range p.Roots {
		if _, duplicate := paths[root.ID]; duplicate || rec.Roots[root.ID] != root.Path {
			return unavailable()
		}
		paths[root.ID] = root.Path
	}
	if len(paths) != len(rec.Roots) {
		return unavailable()
	}
	canonical, err := revive.CanonicalRoots(paths)
	if err != nil {
		return err
	}
	byID := map[string]revive.RootPlan{}
	for _, root := range canonical {
		byID[root.ID] = root
	}
	physical := 0
	for i, root := range p.Roots {
		if root != byID[root.ID] || checkDirectoryChain(root.Path) != nil {
			return unavailable()
		}
		if ref == m.Original && len(m.LogicalRoots) != 0 {
			if len(m.LogicalRoots) != len(p.Roots) || m.LogicalRoots[i].ID != root.ID || matchDirectory(root.Path, m.LogicalRoots[i]) != nil {
				return unavailable()
			}
		}
		if root.SwapRoot != "" {
			continue
		}
		if physical >= len(j.Roots) || physical >= len(m.OriginalRoots) || physical >= len(m.CandidateRoots) {
			return unavailable()
		}
		jr := j.Roots[physical]
		if m.OriginalRoots[physical].ID != root.ID || m.CandidateRoots[physical].ID != root.ID || m.OriginalRoots[physical] == m.CandidateRoots[physical] {
			return unavailable()
		}
		if jr.ID != root.ID || jr.Live != root.Path || jr.Stage != sibling(root.Path, "stage", p.OpID, root.ID) || jr.Prev != sibling(root.Path, "prev", p.OpID, root.ID) || jr.Failed != sibling(root.Path, "failed", p.OpID, root.ID) {
			return unavailable()
		}
		expected := m.OriginalRoots[physical]
		if ref == m.Candidate {
			expected = m.CandidateRoots[physical]
		}
		if expected.ID != root.ID || matchDirectory(root.Path, expected) != nil {
			return unavailable()
		}
		physical++
	}
	if physical != len(j.Roots) || physical != len(m.OriginalRoots) || physical != len(m.CandidateRoots) {
		return unavailable()
	}
	profile := parent.Plan.OriginalProfile
	if ref == m.Candidate {
		profile = parent.Plan.CandidateProfile
	}
	if environments.Digest(p.Profile) != environments.Digest(profile) {
		return unavailable()
	}
	return nil
}

func sibling(root, kind, op, id string) string {
	return filepath.Join(filepath.Dir(root), ".valise-"+kind+"-"+op+"-"+id)
}

func checkDirectoryChain(path string) error {
	real, err := filepath.EvalSymlinks(path)
	if err != nil || real != path {
		return unavailable()
	}
	// Every component below the OS root must remain a real directory. Do not
	// follow a new parent alias into an unrelated data tree.
	current := string(filepath.Separator)
	for _, part := range strings.Split(strings.TrimPrefix(path, current), string(filepath.Separator)) {
		current = filepath.Join(current, part)
		st, err := os.Lstat(current)
		if err != nil || !st.IsDir() {
			return unavailable()
		}
	}
	return nil
}

func matchDirectory(path string, expected revive.ManagedRoot) error {
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !ok || !st.IsDir() || owner.Uid != uint32(os.Getuid()) || st.Mode().Perm()&0022 != 0 || uint64(owner.Dev) != expected.Device || owner.Ino != expected.Inode {
		return unavailable()
	}
	return nil
}
