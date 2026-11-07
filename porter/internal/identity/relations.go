package identity

import (
	"errors"
	"path/filepath"
	"reflect"
	"regexp"
	"time"

	"valise/core/enroll"
	model "valise/core/identity"
)

type RecoveryInput struct {
	OperationRef      string                `json:"operation_ref"`
	Context           model.RecoveryContext `json:"context"`
	SourceDisposition string                `json:"source_disposition"`
	InstallID         string                `json:"install_id"`
	InstanceID        string                `json:"instance_id"`
	Roots             map[string]string     `json:"roots"`
	ProfileDigest     string                `json:"profile_digest"`
}
type recoveryCommit struct {
	Schema   string          `json:"schema"`
	State    string          `json:"state"`
	Input    RecoveryInput   `json:"input"`
	Server   model.Server    `json:"server"`
	Agent    model.Agent     `json:"agent"`
	Relation *model.Relation `json:"relation,omitempty"`
}

var operationPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

func recoveryPath(home string) string      { return filepath.Join(Dir(home), "recovery-commit.json") }
func operationPath(home, op string) string { return filepath.Join(Dir(home), "operations", op+".json") }
func relationPath(home, id string) string  { return filepath.Join(Dir(home), "relations", id+".json") }
func readRecovery(home string) (*recoveryCommit, error) {
	var c recoveryCommit
	ok, err := readJSON(recoveryPath(home), &c)
	if err != nil || !ok {
		return nil, err
	}
	if c.Schema != model.Schema || (c.State != "pending" && c.State != "complete") || !operationPattern.MatchString(c.Input.OperationRef) {
		return nil, reconciliation()
	}
	return &c, nil
}
func checkRecoveryCommit(home string) error {
	c, err := readRecovery(home)
	if err != nil {
		return err
	}
	if c != nil && c.State != "complete" {
		return reconciliation()
	}
	return nil
}

// CompleteRecovery commits a verified, healthy recovery exactly once per
// operation. The caller must derive disposition from the trusted execution
// path, never a user confirmation or a guessed source identity.
func CompleteRecovery(home string, in RecoveryInput, persist func() error) (a *model.Agent, rel *model.Relation, err error) {
	if !operationPattern.MatchString(in.OperationRef) || enroll.ValidInstanceID(in.InstanceID) != nil || enroll.ValidInstanceID(in.InstallID) != nil || in.ProfileDigest == "" || len(in.Roots) == 0 || persist == nil {
		return nil, nil, errors.New("invalid recovery identity facts")
	}
	if in.Context.SourceSnapshot.VaultID == "" || in.Context.SourceSnapshot.SnapshotID == "" {
		return nil, nil, errors.New("recovery snapshot identity is unknown")
	}
	switch in.Context.Mode {
	case "rollback", "migrate", "clone":
	default:
		return nil, nil, errors.New("invalid recovery identity mode")
	}
	switch in.SourceDisposition {
	case model.StoppedVerified, model.ExclusiveOmitted, model.Unresolved:
	default:
		return nil, nil, errors.New("invalid source disposition")
	}
	if ref := in.Context.SourceAgent; ref != nil && (ref.ServerUID == "" || ref.InstallID == "" || ref.InstanceID == "" || ref.Generation == "") {
		return nil, nil, errors.New("incomplete source identity")
	}
	err = WithLock(home, func() error {
		var done recoveryCommit
		found, e := readJSON(operationPath(home, in.OperationRef), &done)
		if e != nil {
			return e
		}
		pending, e := readRecovery(home)
		if e != nil {
			return e
		}
		if found {
			if done.Schema != model.Schema || done.State != "complete" || !reflect.DeepEqual(done.Input, in) {
				return reconciliation()
			}
			if pending != nil && pending.State == "pending" {
				if pending.Input.OperationRef != in.OperationRef {
					return reconciliation()
				}
				if e := writeJSON(recoveryPath(home), &done); e != nil {
					return e
				}
			}
			a, rel = &done.Agent, done.Relation
			return nil
		}
		if c, e := readCommit(home); e != nil {
			return e
		} else if c != nil && c.State != "complete" {
			return reconciliation()
		}
		var c *recoveryCommit
		if pending != nil && pending.State == "pending" {
			if !reflect.DeepEqual(pending.Input, in) {
				return reconciliation()
			}
			c = pending
		} else {
			now := time.Now().UTC()
			s, e := server(home)
			if e != nil {
				return e
			}
			var agent model.Agent
			exists, e := readJSON(agentPath(home, in.InstanceID), &agent)
			if e != nil {
				return e
			}
			if in.Context.Mode == "rollback" {
				// A legacy rollback cannot invent a previously accepted generation.
				if !exists {
					return persist()
				}
				if s == nil || agent.Schema != model.Schema || agent.Ref.ServerUID != s.ServerUID || agent.Ref.InstanceID != in.InstanceID || agent.Ref.InstallID != in.InstallID {
					return reconciliation()
				}
			} else {
				if s == nil {
					uid, e := model.NewID()
					if e != nil {
						return e
					}
					s = &model.Server{Schema: model.Schema, ServerUID: uid, CreatedAt: now}
				}
				gen, e := model.NewID()
				if e != nil {
					return e
				}
				agent = model.Agent{Schema: model.Schema, Ref: model.AgentRef{ServerUID: s.ServerUID, InstallID: in.InstallID, InstanceID: in.InstanceID, Generation: gen}, CreatedAt: now}
			}
			agent.Roots, agent.ProfileDigest, agent.AcceptedAt, agent.State = in.Roots, in.ProfileDigest, now, "active"
			kind := ""
			switch in.Context.Mode {
			case "rollback":
				kind = model.RestoredFrom
			case "clone":
				if in.SourceDisposition == model.ExclusiveOmitted {
					kind = model.CloneFrom
				}
			case "migrate":
				if in.SourceDisposition != model.Unresolved {
					kind = model.MigrationCompleted
				}
			}
			var relation *model.Relation
			if kind != "" {
				id, e := model.NewID()
				if e != nil {
					return e
				}
				relation = &model.Relation{Schema: model.Schema, RelationID: id, Kind: kind, SourceAgent: in.Context.SourceAgent, SourceSnapshot: in.Context.SourceSnapshot, TargetAgent: agent.Ref, OperationRef: in.OperationRef, VerifiedAt: now, SourceDisposition: in.SourceDisposition}
				agent.RelationRefs = append(agent.RelationRefs, id)
			}
			c = &recoveryCommit{Schema: model.Schema, State: "pending", Input: in, Server: *s, Agent: agent, Relation: relation}
			if e := writeJSON(recoveryPath(home), c); e != nil {
				return e
			}
		}
		if e := persist(); e != nil {
			return e
		}
		if e := writeJSON(filepath.Join(Dir(home), "server.json"), &c.Server); e != nil {
			return e
		}
		if e := writeJSON(agentPath(home, in.InstanceID), &c.Agent); e != nil {
			return e
		}
		if c.Relation != nil {
			if e := privateDir(filepath.Join(Dir(home), "relations")); e != nil {
				return e
			}
			if e := writeJSON(relationPath(home, c.Relation.RelationID), c.Relation); e != nil {
				return e
			}
		}
		if e := privateDir(filepath.Join(Dir(home), "operations")); e != nil {
			return e
		}
		c.State = "complete"
		if e := writeJSON(operationPath(home, in.OperationRef), c); e != nil {
			return e
		}
		if e := writeJSON(recoveryPath(home), c); e != nil {
			return e
		}
		a, rel = &c.Agent, c.Relation
		return nil
	})
	return
}

// ListRelations returns committed target-authoritative relations. Missing
// records mean unknown history and never prevent snapshot data restoration.
func ListRelations(home string) (out []model.Relation, err error) {
	out = []model.Relation{}
	err = locked(home, false, func() error {
		if e := checkCommit(home); e != nil {
			return e
		}
		paths, e := filepath.Glob(filepath.Join(Dir(home), "relations", "*.json"))
		if e != nil {
			return e
		}
		for _, path := range paths {
			var rel model.Relation
			ok, e := readJSON(path, &rel)
			if e != nil {
				return e
			}
			if !ok || rel.Schema != model.Schema || rel.RelationID == "" || !operationPattern.MatchString(rel.OperationRef) {
				return reconciliation()
			}
			var op recoveryCommit
			ok, e = readJSON(operationPath(home, rel.OperationRef), &op)
			if e != nil {
				return e
			}
			if !ok || op.State != "complete" || op.Relation == nil || !reflect.DeepEqual(*op.Relation, rel) {
				return reconciliation()
			}
			out = append(out, rel)
		}
		return nil
	})
	return
}
