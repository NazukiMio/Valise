package environments

import (
	"encoding/json"
	"os"

	"valise/core/environment"
	"valise/core/identity"
)

// ReleaseInitial is an internal CAS used only after an explicitly authorized
// cleanup proved that this first native preparation never took over service.
// It cannot remove an environment that was activated or a later incarnation.
func (s Store) ReleaseInitial(agent identity.AgentRef, revision uint64, op string) error {
	unlock, err := s.lock(agent)
	if err != nil {
		return err
	}
	defer unlock()
	r, err := s.Load(agent)
	if err != nil {
		return err
	}
	if r.Revision != revision {
		return ErrConflict
	}
	if r.Active != environment.A || r.Slots[0].Ref != (environment.Ref{Slot: environment.A, Revision: 1}) || r.Slots[0].State != environment.Active || len(r.History) != 0 {
		return environment.ErrState
	}
	if r.Operation == nil {
		if r.Slots[1].Revision != 0 || r.Slots[1].State != environment.Empty {
			return environment.ErrState
		}
	} else {
		o := r.Operation
		if o.ID != op || o.Phase != environment.Cancelled || o.Confirmed || o.OriginalCleaned || o.Original != r.Slots[0].Ref ||
			o.Candidate != (environment.Ref{Slot: environment.B, Revision: 1}) || r.Slots[1].Ref != o.Candidate || r.Slots[1].State != environment.Stopped {
			return environment.ErrState
		}
	}
	p, err := s.policy(agent)
	if err != nil {
		return err
	}
	if p.Revision < r.Revision {
		p.Revision = r.Revision
	}
	if p.Revision == ^uint64(0) {
		return environment.ErrState
	}
	p.Schema, p.Agent, p.Mode = policySchema, agent, r.Mode
	p.Revision++
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if err := s.savePath(s.policyPath(agent), data); err != nil {
		return err
	}
	if err := os.Remove(s.path(agent)); err != nil {
		return err
	}
	d, err := os.Open(s.dir())
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
