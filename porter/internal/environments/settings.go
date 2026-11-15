package environments

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"valise/core/environment"
	"valise/core/identity"
	"valise/core/protection"
)

const policySchema = "valise-environment-policy/1"

type policy struct {
	Schema   string            `json:"schema"`
	Agent    identity.AgentRef `json:"agent"`
	Revision uint64            `json:"revision"`
	Mode     environment.Mode  `json:"mode"`
}

func (s Store) policyPath(a identity.AgentRef) string {
	return filepath.Join(s.dir(), environment.Key(a)+".policy.json")
}

func (s Store) policy(a identity.AgentRef) (policy, error) {
	p := policy{Schema: policySchema, Agent: a, Mode: environment.Parallel}
	f, err := privateFile(s.policyPath(a), false)
	if errors.Is(err, os.ErrNotExist) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	defer f.Close()
	p = policy{}
	b, err := io.ReadAll(io.LimitReader(f, (16<<10)+1))
	if err != nil {
		return p, err
	}
	if len(b) > 16<<10 {
		return p, environment.ErrState
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(&p); err != nil {
		return p, err
	}
	var extra any
	if d.Decode(&extra) != io.EOF || p.Schema != policySchema || p.Agent != a || p.Revision == 0 {
		return p, environment.ErrState
	}
	return p, nil
}

// Settings reads policy without inventing a running A, starting or repairing.
func (s Store) Settings(a identity.AgentRef) (environment.Settings, error) {
	v := environment.Settings{Agent: a, Mode: environment.Parallel}
	if err := protection.ValidateAgent(a); err != nil {
		return v, err
	}
	if err := s.directories(false); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			v.Digest = v.Stamp()
			return v, nil
		}
		return v, err
	}
	r, err := s.Load(a)
	if err == nil {
		v.Mode, v.Managed, v.Revision = r.Mode, true, r.Revision
		v.Busy = r.Operation != nil && !environment.OperationFinished(r.Operation.Phase)
	} else if errors.Is(err, os.ErrNotExist) {
		p, e := s.policy(a)
		if e != nil {
			return v, e
		}
		v.Mode, v.Revision = p.Mode, p.Revision
	} else {
		return v, err
	}
	v.Digest = v.Stamp()
	return v, v.Validate()
}

// SetSettings is called while the server mutation lock protects accepted
// identity facts. The same per-Agent lock also serializes registry adoption.
func (s Store) SetSettings(p environment.SetSettingsParams) (environment.Settings, error) {
	var zero environment.Settings
	if protection.ValidateAgent(p.Agent) != nil || p.Expected == "" || p.Mode != environment.Parallel && p.Mode != environment.Compact && p.Mode != environment.Direct {
		return zero, environment.ErrState
	}
	unlock, err := s.lock(p.Agent)
	if err != nil {
		return zero, err
	}
	defer unlock()
	v, err := s.Settings(p.Agent)
	if err != nil {
		return v, err
	}
	if v.Digest != p.Expected {
		return v, ErrConflict
	}
	if v.Busy || v.Revision == ^uint64(0) {
		return v, environment.ErrState
	}
	if v.Mode == p.Mode {
		return v, nil
	}
	if v.Managed {
		r, err := s.Load(p.Agent)
		if err != nil {
			return v, err
		}
		r, err = r.SetMode(p.Mode)
		if err != nil {
			return v, err
		}
		b, err := json.Marshal(r)
		if err != nil {
			return v, err
		}
		if err := s.save(p.Agent, b); err != nil {
			return v, err
		}
	} else {
		b, err := json.Marshal(policy{policySchema, p.Agent, v.Revision + 1, p.Mode})
		if err != nil {
			return v, err
		}
		if err := s.savePath(s.policyPath(p.Agent), b); err != nil {
			return v, err
		}
	}
	return s.Settings(p.Agent)
}
