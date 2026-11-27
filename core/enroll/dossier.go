package enroll

import (
	"time"

	"valise/core/profile"
)

// DossierSchema is the instance dossier's schema (docs/ARCHITECTURE.md 4.1).
const DossierSchema = "valise-dossier/1"

// Dossier is the instance dossier: the facts of one harness installation
// on a server, beside the general profile's rules. It holds no secrets.
type Dossier struct {
	Schema    string    `json:"schema"`
	InstallID string    `json:"install_id"`
	ProfileID string    `json:"profile_id"`
	Version   int       `json:"profile_version"`
	UpdatedAt time.Time `json:"updated_at"`
	// Installation facts, from the default agent's detection.
	Provenance Provenance     `json:"provenance"`
	Service    []ServiceState `json:"service"`
	Entries    []EntryState   `json:"entries"`
	Agents     []DossierAgent `json:"agents"`
}

// DossierAgent is one agent of the installation.
type DossierAgent struct {
	InstanceID     string              `json:"instance_id"`
	Name           string              `json:"name,omitempty"`
	Env            map[string]string   `json:"env,omitempty"`
	Roots          map[string]string   `json:"roots"`
	LocalOverrides []profile.Override  `json:"local_overrides"`
	LocalSQLite    []profile.SQLiteRef `json:"local_sqlite,omitempty"`
	LocalRoots     []LocalRoot         `json:"local_roots,omitempty"`
	ExternalRefs   []ExternalRef       `json:"external_refs,omitempty"`
	ToolDeps       []ToolDep           `json:"tool_deps,omitempty"`
	EntryPorts     map[string]int      `json:"entry_ports,omitempty"`
	LocalEntries   []LocalEntry        `json:"local_entries,omitempty"`
	EnrolledAt     time.Time           `json:"enrolled_at"`
	Superseded     *Supersession       `json:"superseded,omitempty"`
	// Service and Entries are a named agent's own (empty unless its
	// service is shared, see profile.ForAgent).
	Service []ServiceState `json:"service,omitempty"`
	// Names: the user's (display_name) and the harness's (UI.md 9.5).
	DisplayName   string   `json:"display_name,omitempty"`
	HarnessName   string   `json:"harness_name,omitempty"`
	HarnessAgents []string `json:"harness_agents,omitempty"`
}

// Agent returns the agent with id, nil when none.
func (d *Dossier) Agent(id string) *DossierAgent {
	for i := range d.Agents {
		if d.Agents[i].InstanceID == id {
			return &d.Agents[i]
		}
	}
	return nil
}

// Record is the agent's instance record (the view the rest of Valise uses).
func (d *Dossier) Record(id string) *Record {
	a := d.Agent(id)
	if a == nil {
		return nil
	}
	r := &Record{InstanceID: a.InstanceID, ProfileID: d.ProfileID, ProfileVersion: d.Version, Roots: a.Roots,
		Provenance: d.Provenance, Service: d.Service, Entries: d.Entries, LocalOverrides: a.LocalOverrides,
		EntryPorts: a.EntryPorts, EnrolledAt: a.EnrolledAt, Superseded: a.Superseded, Name: a.Name, Env: a.Env,
		DisplayName: a.DisplayName, HarnessName: a.HarnessName, HarnessAgents: a.HarnessAgents}
	if len(a.LocalSQLite) > 0 {
		r.LocalSQLite = append([]profile.SQLiteRef(nil), a.LocalSQLite...)
	}
	if len(a.LocalRoots) > 0 {
		r.LocalRoots = append([]LocalRoot(nil), a.LocalRoots...)
	}
	if len(a.ExternalRefs) > 0 {
		r.ExternalRefs = append([]ExternalRef(nil), a.ExternalRefs...)
	}
	if len(a.ToolDeps) > 0 {
		r.ToolDeps = append([]ToolDep(nil), a.ToolDeps...)
	}
	if len(a.LocalEntries) > 0 {
		r.LocalEntries = append([]LocalEntry(nil), a.LocalEntries...)
	}
	if a.Name != "" {
		r.Provenance, r.Service, r.Entries = Provenance{}, a.Service, []EntryState{}
	}
	if r.LocalOverrides == nil {
		r.LocalOverrides = []profile.Override{}
	}
	return r
}

// Put stores a record into the dossier: the agent's own fields, and, for
// the default agent, the installation facts.
func (d *Dossier) Put(r Record, now time.Time) {
	d.Schema, d.ProfileID, d.Version, d.UpdatedAt = DossierSchema, r.ProfileID, r.ProfileVersion, now.UTC()
	if d.InstallID == "" {
		d.InstallID = r.ProfileID
	}
	a := DossierAgent{InstanceID: r.InstanceID, Name: r.Name, Env: r.Env, Roots: r.Roots, LocalOverrides: r.LocalOverrides,
		EntryPorts: r.EntryPorts, EnrolledAt: r.EnrolledAt, Superseded: r.Superseded,
		DisplayName: r.DisplayName, HarnessName: r.HarnessName, HarnessAgents: r.HarnessAgents}
	if len(r.LocalSQLite) > 0 {
		a.LocalSQLite = append([]profile.SQLiteRef(nil), r.LocalSQLite...)
	}
	if len(r.LocalRoots) > 0 {
		a.LocalRoots = append([]LocalRoot(nil), r.LocalRoots...)
	}
	if len(r.ExternalRefs) > 0 {
		a.ExternalRefs = append([]ExternalRef(nil), r.ExternalRefs...)
	}
	if len(r.ToolDeps) > 0 {
		a.ToolDeps = append([]ToolDep(nil), r.ToolDeps...)
	}
	if len(r.LocalEntries) > 0 {
		a.LocalEntries = append([]LocalEntry(nil), r.LocalEntries...)
	}
	if r.Name == "" {
		d.Provenance, d.Service, d.Entries = r.Provenance, r.Service, r.Entries
	} else {
		a.Service = r.Service
	}
	if old := d.Agent(r.InstanceID); old != nil {
		if a.DisplayName == "" {
			a.DisplayName = old.DisplayName // a re-enrollment keeps the user's name
		}
		if r.LocalSQLite == nil && len(old.LocalSQLite) > 0 {
			a.LocalSQLite = append([]profile.SQLiteRef(nil), old.LocalSQLite...)
		}
		if r.LocalRoots == nil && len(old.LocalRoots) > 0 {
			a.LocalRoots = append([]LocalRoot(nil), old.LocalRoots...)
			if a.Roots == nil {
				a.Roots = map[string]string{}
			}
			for _, lr := range a.LocalRoots {
				if lr.ID != "" && lr.Path != "" {
					if _, exists := a.Roots[lr.ID]; !exists {
						a.Roots[lr.ID] = lr.Path
					}
				}
			}
		}
		if r.ExternalRefs == nil && len(old.ExternalRefs) > 0 {
			a.ExternalRefs = append([]ExternalRef(nil), old.ExternalRefs...)
		}
		if r.ToolDeps == nil && len(old.ToolDeps) > 0 {
			a.ToolDeps = append([]ToolDep(nil), old.ToolDeps...)
		}
		if r.LocalEntries == nil && len(old.LocalEntries) > 0 {
			a.LocalEntries = append([]LocalEntry(nil), old.LocalEntries...)
		}
		*old = a
		return
	}
	d.Agents = append(d.Agents, a)
}

// Remove drops an agent; it reports whether any agent is left.
func (d *Dossier) Remove(id string) bool {
	for i := range d.Agents {
		if d.Agents[i].InstanceID == id {
			d.Agents = append(d.Agents[:i], d.Agents[i+1:]...)
			break
		}
	}
	return len(d.Agents) > 0
}
