package runtimeguard

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"valise/core/enroll"
	"valise/core/environment"
	"valise/core/identity"
	"valise/core/profile"
	"valise/core/protection"
	"valise/core/revive"
)

const stateDir = ".local/state/valise"

var rootName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

type preparation struct {
	Schema      string                            `json:"schema"`
	Params      revive.EnvironmentPlanParams      `json:"params"`
	Plan        revive.Plan                       `json:"plan"`
	State       string                            `json:"state"`
	OperationID string                            `json:"operation_id"`
	Native      struct{ Unit, Executable string } `json:"native"`
}

func load(home string) ([]occupation, error) {
	var out []occupation
	rel := filepath.Join(stateDir, "environments")
	if err := occupationDirectory(home, rel); err == nil {
		entries, err := os.ReadDir(filepath.Join(home, rel))
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			name := entry.Name()
			if !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".policy.json") {
				continue
			}
			var r environment.Registry
			if err := read(home, filepath.Join(rel, name), &r, 16<<20); err != nil {
				return nil, err
			}
			if r.Validate() != nil || name != environment.Key(r.Agent)+".json" {
				return nil, unreadable()
			}
			rec, p, err := accepted(home, r.Agent.InstallID, r.Agent.InstanceID)
			if err != nil || rec == nil {
				return nil, unreadable()
			}
			scope := Record(*rec, p)
			scope.Paths = append(scope.Paths, filepath.Join(home, stateDir, "programs", environment.Key(r.Agent)))
			scope.Units = append(scope.Units, profile.ServiceUnit{Scope: "user", Unit: "valise-agent-" + environment.Key(r.Agent) + ".service"})
			out = append(out, occupation{instance: r.Agent.InstanceID, install: r.Agent.InstallID, paths: scope.Paths, units: scope.Units})
		}
	} else if !isMissing(err) {
		return nil, err
	}
	rel = filepath.Join(stateDir, "environment-preparations")
	if err := occupationDirectory(home, rel); isMissing(err) {
		return out, nil
	} else if err != nil {
		return nil, err
	}
	agents, err := os.ReadDir(filepath.Join(home, rel))
	if err != nil {
		return nil, err
	}
	for _, agent := range agents {
		dir := filepath.Join(rel, agent.Name())
		if err := directory(home, dir); err != nil {
			return nil, err
		}
		entries, err := os.ReadDir(filepath.Join(home, dir))
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			var p preparation
			if err := read(home, filepath.Join(dir, entry.Name()), &p, 96<<20); err != nil {
				return nil, err
			}
			if p.Schema != "valise-environment-preparation/1" || protection.ValidateAgent(p.Params.Agent) != nil || agent.Name() != environment.Key(p.Params.Agent) || entry.Name() != p.OperationID+".json" || !protection.ValidID(p.OperationID) {
				return nil, unreadable()
			}
			if p.State == "cleaned" {
				continue
			}
			if !slices.Contains([]string{"capturing", "original_ready", "registering", "data_ready", "building", "probing", "binding", "prepared", "failed", "cancelling", "cancelled", "cleaning"}, p.State) || p.Plan.InstanceID != p.Params.Agent.InstanceID || !revive.ValidOpID(p.Plan.OpID) || len(p.Plan.Roots) == 0 || p.Native.Unit == "" || !filepath.IsAbs(p.Native.Executable) {
				return nil, unreadable()
			}
			rec, acceptedProfile, err := accepted(home, p.Params.Agent.InstallID, p.Params.Agent.InstanceID)
			if err != nil {
				return nil, unreadable()
			}
			scope := Record(*rec, acceptedProfile)
			for _, root := range p.Plan.Roots {
				if !rootName.MatchString(root.ID) || !filepath.IsAbs(root.Path) || filepath.Clean(root.Path) != root.Path {
					return nil, unreadable()
				}
				scope.Paths = append(scope.Paths, root.Path)
				if root.SwapRoot == "" {
					for _, kind := range []string{"stage", "prev", "failed"} {
						scope.Paths = append(scope.Paths, filepath.Join(filepath.Dir(root.Path), ".valise-"+kind+"-"+p.Plan.OpID+"-"+root.ID))
					}
				}
			}
			scope.Paths = append(scope.Paths, filepath.Join(home, stateDir, "programs", agent.Name()), filepath.Join(home, dir, p.OperationID))
			if p.Native.Executable != "" {
				scope.Paths = append(scope.Paths, p.Native.Executable)
			}
			if runtime := p.Params.Input.Header.Runtime; runtime != nil {
				for _, dep := range runtime.Dependencies {
					if dep.Path != "" {
						scope.Paths = append(scope.Paths, dep.Path)
					}
					if dep.Resolved != "" {
						scope.Paths = append(scope.Paths, dep.Resolved)
					}
				}
			}
			if p.Native.Unit != "" {
				scope.Units = append(scope.Units, profile.ServiceUnit{Scope: "user", Unit: p.Native.Unit})
			}
			scope.Units = append(scope.Units, profile.ServiceUnit{Scope: "user", Unit: "valise-agent-" + agent.Name() + ".service"})
			out = append(out, occupation{instance: p.Params.Agent.InstanceID, install: p.Params.Agent.InstallID, paths: scope.Paths, units: scope.Units})
		}
	}
	return out, nil
}

// An unmanaged installation has no ownership metadata to trust. Do not make
// its existing operations depend on permissions needed only for a future
// private environment. Once metadata exists, validate its complete ancestry.
func occupationDirectory(home, rel string) error {
	if !filepath.IsAbs(home) || filepath.Clean(home) != home {
		return unreadable()
	}
	if _, err := os.Lstat(filepath.Join(home, rel)); err != nil {
		return err
	}
	return directory(home, rel)
}

// Record describes metadata and code effects. Labels alone deliberately do
// not call it: they neither move data nor change the running entry.
func Record(rec enroll.Record, p *profile.Profile) Scope {
	s := Scope{Instances: []string{rec.InstanceID}}
	for _, path := range rec.Roots {
		s.Paths = append(s.Paths, path)
	}
	s.Paths = append(s.Paths, rec.Provenance.BodyPaths()...)
	if p != nil {
		effective := p.ForAgent(rec.Name)
		if effective.Service != nil {
			s.Units = append(s.Units, effective.Service.Candidates...)
		}
	}
	return s
}

func accepted(home, install, id string) (*enroll.Record, *profile.Profile, error) {
	if enroll.ValidInstanceID(install) != nil || enroll.ValidInstanceID(id) != nil {
		return nil, nil, unreadable()
	}

	var d enroll.Dossier
	rel := filepath.Join(".config/valise/dossiers", install+".json")
	if err := read(home, rel, &d, 16<<20); err != nil {
		return nil, nil, err
	}
	if d.Schema != enroll.DossierSchema || d.InstallID != install {
		return nil, nil, unreadable()
	}
	rec := d.Record(id)
	if rec == nil {
		return nil, nil, unreadable()
	}
	var p profile.Profile
	if err := read(home, filepath.Join(".config/valise/dossiers", install+".profile.json"), &p, 2<<20); err != nil {
		return nil, nil, err
	}
	return rec, &p, nil
}

// Instance reads only committed metadata. It never imports old dossiers or
// takes an identity lock (enrollment may already hold that lock).
func Instance(home, id string) (Scope, error) {
	s := Scope{Instances: []string{id}}
	if enroll.ValidInstanceID(id) != nil {
		return s, unreadable()
	}
	var a identity.Agent
	err := read(home, filepath.Join(".config/valise/identity/agents", id+".json"), &a, 1<<20)
	if err == nil {
		if protection.ValidateAgent(a.Ref) != nil || a.Ref.InstanceID != id {
			return s, unreadable()
		}
		rec, p, err := accepted(home, a.Ref.InstallID, id)
		if err != nil {
			return s, err
		}
		return Record(*rec, p), nil
	}
	if !isMissing(err) {
		return s, err
	}
	rel := ".config/valise/dossiers"
	if err := directory(home, rel); err == nil {
		entries, err := os.ReadDir(filepath.Join(home, rel))
		if err != nil {
			return s, err
		}
		for _, entry := range entries {
			name := entry.Name()
			if !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".profile.json") {
				continue
			}
			var d enroll.Dossier
			if err := read(home, filepath.Join(rel, name), &d, 16<<20); err != nil {
				return s, err
			}
			if d.Record(id) != nil {
				rec, p, err := accepted(home, d.InstallID, id)
				if err != nil {
					return s, err
				}
				return Record(*rec, p), nil
			}
		}
	} else if !isMissing(err) {
		return s, err
	}
	var rec enroll.Record
	if err := read(home, filepath.Join(".config/valise/instances", id+".json"), &rec, 2<<20); isMissing(err) {
		return s, nil
	} else if err != nil {
		return s, err
	}
	var p profile.Profile
	if err := read(home, filepath.Join(".config/valise/instances", id+".profile.json"), &p, 2<<20); err != nil && !isMissing(err) {
		return s, err
	}
	return Record(rec, &p), nil
}
