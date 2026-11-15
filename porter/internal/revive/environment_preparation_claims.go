package revive

import (
	"os"
	"path/filepath"

	"valise/core/environment"
	"valise/core/revive"
	"valise/porter/internal/environments"
)

type preparationClaim struct {
	Kind     string             `json:"kind"`
	Root     string             `json:"root,omitempty"`
	Identity revive.ManagedRoot `json:"identity"`
	Removed  bool               `json:"removed,omitempty"`
}

func preparationResource(home string, p *EnvironmentPreparation, kind, root string) (string, error) {
	switch kind {
	case "reuse":
		_, path, err := (&Runner{Store: Store{Home: home}}).reuseCleanupItem(p, root)
		return path, err
	case "workspace":
		dir, err := preparationDir(home, p.Params.Agent, false)
		if err != nil {
			return "", err
		}
		return filepath.Join(dir, p.OperationID), nil
	case "probe":
		dir, err := preparationResource(home, p, "workspace", "")
		if err != nil || p.ProbeWorkspace == "" {
			return "", managedDataError()
		}
		return filepath.Join(dir, p.ProbeWorkspace), nil
	case "program-original":
		if p.ManagedSource != nil {
			return (environments.ProgramStore{Home: home}).RevisionPath(p.Params.Agent, p.Original)
		}
		return (environments.ProgramStore{Home: home}).RevisionPath(p.Params.Agent, environment.Ref{Slot: environment.A, Revision: 1})
	case "program-candidate":
		if (p.Candidate.Slot != environment.A && p.Candidate.Slot != environment.B) || p.Candidate.Revision == 0 {
			return "", managedDataError()
		}
		return (environments.ProgramStore{Home: home}).RevisionPath(p.Params.Agent, p.Candidate)
	case "stage":
		for _, r := range p.Plan.Roots {
			if r.ID == root && r.SwapRoot == "" {
				return sibling(r.Path, "stage", p.Plan.OpID, r.ID), nil
			}
		}
	}
	return "", managedDataError()
}

func (r *Runner) claimPreparation(p *EnvironmentPreparation, kind, root, path string, expected *revive.ManagedRoot) error {
	want, err := preparationResource(r.Store.Home, p, kind, root)
	if err != nil || path != want {
		return managedDataError()
	}
	id := kind + ":" + root
	fact, err := managedRootIdentity(id, path)
	if err != nil {
		return err
	}
	if expected != nil && (expected.Device != fact.Device || expected.Inode != fact.Inode) {
		return managedDataError()
	}
	st, err := os.Lstat(path)
	if err != nil || st.Mode().Perm()&0077 != 0 {
		return managedDataError()
	}
	for _, claim := range p.Claims {
		if claim.Kind == kind && claim.Root == root {
			if claim.Identity != fact || claim.Removed {
				return managedDataError()
			}
			return nil
		}
	}
	p.Claims = append(p.Claims, preparationClaim{Kind: kind, Root: root, Identity: fact})
	return savePreparation(r.Store.Home, p, false)
}

func (r *Runner) preparationPrograms(p *EnvironmentPreparation) environments.ProgramStore {
	return environments.ProgramStore{Home: r.Store.Home, Claim: func(ref environment.Ref, path string) error {
		if p.ManagedSource != nil {
			if ref != p.Candidate {
				return managedDataError()
			}
			return r.claimPreparation(p, "program-candidate", "", path, nil)
		}
		kind := "program-original"
		if ref != (environment.Ref{Slot: environment.A, Revision: 1}) {
			if ref != p.Candidate {
				return managedDataError()
			}
			kind = "program-candidate"
		}
		return r.claimPreparation(p, kind, "", path, nil)
	}}
}

func validPreparationClaims(p *EnvironmentPreparation) bool {
	seen := map[string]bool{}
	for _, claim := range p.Claims {
		if p.ManagedSource != nil && claim.Kind == "program-original" {
			return false
		}
		key := claim.Kind + ":" + claim.Root
		if seen[key] || claim.Identity.ID != key || claim.Identity.Inode == 0 {
			return false
		}
		seen[key] = true
		switch claim.Kind {
		case "workspace", "probe", "program-original", "program-candidate":
			if claim.Root != "" {
				return false
			}
		case "stage":
			found := false
			for _, root := range p.Plan.Roots {
				found = found || root.ID == claim.Root && root.SwapRoot == ""
			}
			if !found {
				return false
			}
		default:
			return false
		}
	}
	return len(p.Claims) <= len(p.Plan.Roots)+4
}
