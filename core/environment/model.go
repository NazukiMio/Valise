// Package environment models private restore environments, outside the public
// vault format. Its transitions record executor evidence; they execute nothing
// and must never be exposed as an untrusted event-submission RPC.
package environment

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"valise/core/identity"
	"valise/core/protection"
	"valise/core/vault"
)

const Schema = "valise-environments/1"

type Mode string

const (
	Parallel Mode = "parallel"
	Compact  Mode = "compact"
	Direct   Mode = "direct"
)

type SlotID string

const (
	A SlotID = "A"
	B SlotID = "B"
)

type State string

const (
	Empty     State = "empty"
	Active    State = "active"
	Stopped   State = "stopped"
	Preparing State = "preparing"
	Ready     State = "ready"
	Unknown   State = "unknown"
)

type Phase string

const (
	Building      Phase = "building"
	Prepared      Phase = "prepared"
	Stopping      Phase = "stopping"
	OriginalDown  Phase = "original_down"
	Starting      Phase = "starting"
	AwaitingHuman Phase = "awaiting_human"
	Accepted      Phase = "accepted"
	RollingBack   Phase = "rolling_back"
	CandidateDown Phase = "candidate_down"
	Resuming      Phase = "resuming"
	RolledBack    Phase = "rolled_back"
	Cancelled     Phase = "cancelled"
	Cleaning      Phase = "cleaning"
)

var (
	ErrState = errors.New("environment: state or evidence does not permit this action")
	ErrStale = errors.New("environment: operation or environment revision changed")
)

// Ref names a particular incarnation of a reusable slot.
type Ref struct {
	Slot     SlotID `json:"slot"`
	Revision uint64 `json:"revision"`
}

type Slot struct {
	Ref
	State State `json:"state"`
}

type Operation struct {
	ID               string               `json:"id"`
	Snapshot         identity.SnapshotRef `json:"snapshot"`
	Mode             Mode                 `json:"mode"`
	Original         Ref                  `json:"original"`
	Candidate        Ref                  `json:"candidate"`
	Phase            Phase                `json:"phase"`
	FailureCode      string               `json:"failure_code,omitempty"`
	Confirmed        bool                 `json:"confirmed"`
	OriginalCleaned  bool                 `json:"original_cleaned"`
	CandidateCleaned bool                 `json:"candidate_cleaned,omitempty"`
}

type Registry struct {
	Schema    string            `json:"schema"`
	Agent     identity.AgentRef `json:"agent"`
	Revision  uint64            `json:"revision"`
	Mode      Mode              `json:"mode"`
	Active    SlotID            `json:"active"`
	Slots     [2]Slot           `json:"slots"`
	Operation *Operation        `json:"operation,omitempty"`
	History   []Operation       `json:"history,omitempty"`
}

// New records an already verified initial active environment. Importing its
// files and service is an executor responsibility, not an effect of New.
func New(agent identity.AgentRef) (Registry, error) {
	r := Registry{Schema: Schema, Agent: agent, Revision: 1, Mode: Parallel, Active: A,
		Slots: [2]Slot{{Ref{A, 1}, Active}, {Ref{B, 0}, Empty}}}
	return r, r.Validate()
}

// Key is the filesystem-safe key of the complete identity, including generation.
func Key(agent identity.AgentRef) string {
	b, _ := json.Marshal(agent)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func validMode(m Mode) bool { return m == Parallel || m == Compact || m == Direct }

func validFailureCode(s string) bool {
	return len(s) <= 80 && strings.IndexFunc(s, func(c rune) bool {
		return c != '_' && (c < 'a' || c > 'z') && (c < '0' || c > '9')
	}) < 0
}

func validOperation(o Operation) bool {
	return protection.ValidID(o.ID) && validMode(o.Mode) && o.Mode != Direct && o.Original.Revision > 0 && o.Candidate.Revision > 0 &&
		(o.Original.Slot == A || o.Original.Slot == B) && (o.Candidate.Slot == A || o.Candidate.Slot == B) && o.Original.Slot != o.Candidate.Slot &&
		vault.ValidID(o.Snapshot.VaultID) && vault.ValidID(o.Snapshot.SnapshotID) && validFailureCode(o.FailureCode)
}
func (r *Registry) slot(s SlotID) *Slot {
	if s == A {
		return &r.Slots[0]
	}
	if s == B {
		return &r.Slots[1]
	}
	return nil
}

func (r Registry) Validate() error {
	if r.Schema != Schema || r.Revision == 0 || !validMode(r.Mode) || protection.ValidateAgent(r.Agent) != nil {
		return ErrState
	}
	active := 0
	for i, s := range r.Slots {
		if s.Slot != []SlotID{A, B}[i] || !slices.Contains([]State{Empty, Active, Stopped, Preparing, Ready, Unknown}, s.State) || (s.Revision == 0 && s.State != Empty) {
			return ErrState
		}
		if s.State == Active {
			active++
			if s.Slot != r.Active {
				return ErrState
			}
		}
	}
	if active > 1 || (active == 0 && r.Active != "") {
		return ErrState
	}
	ids := map[string]bool{}
	for _, o := range r.History {
		if o.CandidateCleaned && o.Phase != Cancelled && o.Phase != RolledBack {
			return ErrState
		}
		if !validOperation(o) || !terminal(o.Phase) || ids[o.ID] || o.Confirmed != (o.Phase == Accepted) || (o.Phase == RolledBack || o.Phase == Cancelled) && o.FailureCode == "" || o.OriginalCleaned && !o.Confirmed {
			return ErrState
		}
		ids[o.ID] = true
	}
	if o := r.Operation; o != nil {
		if o.CandidateCleaned && o.Phase != Cancelled && o.Phase != RolledBack {
			return ErrState
		}
		if !validOperation(*o) || ids[o.ID] {
			return ErrState
		}
		if r.slot(o.Candidate.Slot).Ref != o.Candidate || (!o.OriginalCleaned && r.slot(o.Original.Slot).Ref != o.Original) {
			return ErrState
		}
		x, y := r.slot(o.Original.Slot), r.slot(o.Candidate.Slot)
		valid := false
		switch o.Phase {
		case Building:
			valid = x.State == Active && y.State == Preparing
		case Prepared:
			valid = x.State == Active && y.State == Ready
		case Stopping:
			valid = x.State == Unknown && y.State == Ready
		case OriginalDown:
			valid = x.State == Stopped && y.State == Ready
		case Starting:
			valid = x.State == Stopped && y.State == Unknown
		case AwaitingHuman, Accepted, Cleaning:
			valid = y.State == Active && (x.State == Stopped || o.OriginalCleaned && x.State == Empty)
		case RollingBack:
			valid = (x.State == Active || x.State == Stopped || x.State == Unknown) && y.State == Unknown && o.FailureCode != ""
		case CandidateDown:
			valid = (x.State == Active || x.State == Stopped || x.State == Unknown) && y.State == Stopped && o.FailureCode != ""
		case Resuming:
			valid = x.State == Unknown && y.State == Stopped && o.FailureCode != ""
		case RolledBack, Cancelled:
			valid = x.State == Active && (y.State == Stopped || o.CandidateCleaned && y.State == Empty) && o.FailureCode != ""
		}
		if !valid || o.Confirmed != (o.Phase == Accepted || o.Phase == Cleaning) || (o.OriginalCleaned && !o.Confirmed) {
			return ErrState
		}
	} else if active != 1 {
		return ErrState
	}
	return nil
}

// change works on a deep copy: failed validation never mutates its input.
func (r Registry) change(fn func(*Registry) error) (Registry, error) {
	if err := r.Validate(); err != nil {
		return r, err
	}
	n := r
	n.History = slices.Clone(r.History)
	if r.Operation != nil {
		o := *r.Operation
		n.Operation = &o
	}
	if err := fn(&n); err != nil {
		return r, err
	}
	n.Revision++
	if n.Revision == 0 || n.Validate() != nil {
		return r, ErrState
	}
	return n, nil
}
