package identity

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"time"

	"valise/core/enroll"
	model "valise/core/identity"
)

type AcceptInput struct {
	InstallID     string            `json:"install_id"`
	InstanceID    string            `json:"instance_id"`
	Roots         map[string]string `json:"roots"`
	ProfileDigest string            `json:"profile_digest"`
	FactsDigest   string            `json:"facts_digest,omitempty"`
	Existing      bool              `json:"existing"`
	Superseded    bool              `json:"superseded"`
}

type commit struct {
	Schema string       `json:"schema"`
	State  string       `json:"state"`
	Input  AcceptInput  `json:"input"`
	Server model.Server `json:"server"`
	Agent  model.Agent  `json:"agent"`
}

func commitPath(home string) string { return filepath.Join(Dir(home), "commit.json") }
func readCommit(home string) (*commit, error) {
	var c commit
	ok, err := readJSON(commitPath(home), &c)
	if err != nil || !ok {
		return nil, err
	}
	if c.Schema != model.Schema || (c.State != "pending" && c.State != "complete") ||
		c.Server.Schema != model.Schema || c.Server.ServerUID == "" || c.Server.CreatedAt.IsZero() ||
		c.Agent.Schema != model.Schema || c.Agent.Ref.ServerUID != c.Server.ServerUID ||
		c.Agent.Ref.InstanceID != c.Input.InstanceID || c.Agent.Ref.InstallID != c.Input.InstallID ||
		c.Agent.Ref.Generation == "" || c.Agent.CreatedAt.IsZero() || c.Agent.AcceptedAt.IsZero() ||
		c.Agent.ProfileDigest != c.Input.ProfileDigest || !reflect.DeepEqual(c.Agent.Roots, c.Input.Roots) {
		return nil, reconciliation()
	}
	return &c, nil
}
func checkCommit(home string) error {
	if err := checkRecoveryCommit(home); err != nil {
		return err
	}
	c, err := readCommit(home)
	if err != nil {
		return err
	}
	if c != nil && c.State != "complete" {
		return reconciliation()
	}
	return nil
}

// Accept is for an explicit user acceptance only. persist must write all accepted
// dossier/profile facts and return any failure; it must not acquire this lock.
func Accept(home string, in AcceptInput, persist func() error) (a *model.Agent, err error) {
	err = WithLock(home, func() error { var e error; a, e = AcceptLocked(home, in, persist); return e })
	return
}

// AcceptLocked is Accept for callers already holding WithLock, so their existing
// dossier lookup and generation decision share the same enrollment lock.
func AcceptLocked(home string, in AcceptInput, persist func() error) (*model.Agent, error) {
	if err := checkRecoveryCommit(home); err != nil {
		return nil, err
	}
	if err := enroll.ValidInstanceID(in.InstanceID); err != nil {
		return nil, err
	}
	if err := enroll.ValidInstanceID(in.InstallID); err != nil {
		return nil, err
	}
	if in.ProfileDigest == "" || len(in.Roots) == 0 || persist == nil {
		return nil, errors.New("accepted identity facts are incomplete")
	}
	// Copy maps so callers cannot mutate a prepared commit after acceptance.
	raw, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	c, err := readCommit(home)
	if err != nil {
		return nil, err
	}
	if c != nil && c.State == "pending" {
		// Only explicit acceptance of the same facts may retry an interrupted commit.
		want, had := in, c.Input
		want.Existing, want.Superseded = had.Existing, had.Superseded
		if !reflect.DeepEqual(want, had) {
			return nil, reconciliation()
		}
	} else {
		now := time.Now().UTC()
		s, err := server(home)
		if err != nil {
			return nil, err
		}
		if s == nil {
			uid, err := model.NewID()
			if err != nil {
				return nil, err
			}
			s = &model.Server{Schema: model.Schema, ServerUID: uid, CreatedAt: now}
		}
		var a model.Agent
		found, err := readJSON(agentPath(home, in.InstanceID), &a)
		if err != nil {
			return nil, err
		}
		if found && (a.Schema != model.Schema || a.Ref.ServerUID != s.ServerUID || a.Ref.InstanceID != in.InstanceID || a.Ref.Generation == "") {
			return nil, errors.New("invalid prior agent identity")
		}
		if !found || !in.Existing || in.Superseded || a.Ref.InstallID != in.InstallID {
			gen, err := model.NewID()
			if err != nil {
				return nil, err
			}
			a = model.Agent{Schema: model.Schema, Ref: model.AgentRef{ServerUID: s.ServerUID, InstallID: in.InstallID, InstanceID: in.InstanceID, Generation: gen}, CreatedAt: now}
		}
		a.Roots, a.ProfileDigest, a.State, a.AcceptedAt = in.Roots, in.ProfileDigest, "active", now
		c = &commit{Schema: model.Schema, State: "pending", Input: in, Server: *s, Agent: a}
		if err := writeJSON(commitPath(home), c); err != nil {
			return nil, err
		}
	}
	if err := persist(); err != nil {
		return nil, err
	}
	if err := writeJSON(filepath.Join(Dir(home), "server.json"), &c.Server); err != nil {
		return nil, err
	}
	if err := writeJSON(agentPath(home, in.InstanceID), &c.Agent); err != nil {
		return nil, err
	}
	c.State = "complete"
	if err := writeJSON(commitPath(home), c); err != nil {
		return nil, err
	}
	return &c.Agent, nil
}
