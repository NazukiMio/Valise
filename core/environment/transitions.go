package environment

import (
	"slices"

	"valise/core/identity"
	"valise/core/protection"
	"valise/core/vault"
)

func terminal(p Phase) bool { return p == Accepted || p == RolledBack || p == Cancelled }

// OperationFinished includes a preparation cancelled without touching A. It
// says nothing about service or gateway health.
func OperationFinished(p Phase) bool { return terminal(p) }

// CancelPreparation records verified absence of a formal candidate and an
// unchanged original before any stop/exchange intent. No health is invented.
func (r Registry) CancelPreparation(id string) (Registry, error) {
	return r.change(func(n *Registry) error {
		o := n.Operation
		if o == nil || o.ID != id {
			return ErrStale
		}
		if o.Phase != Building && o.Phase != Prepared || n.Active != o.Original.Slot || n.slot(o.Original.Slot).State != Active {
			return ErrState
		}
		n.slot(o.Candidate.Slot).State = Stopped
		o.Phase, o.FailureCode = Cancelled, "preparation_cancelled"
		return nil
	})
}

// PreparationCleaned follows explicit removal of only this cancelled
// candidate's owned resources. The current original is never cleared.
func (r Registry) PreparationCleaned(id string, candidate Ref) (Registry, error) {
	if r.Operation == nil || r.Operation.Candidate != candidate {
		return r, ErrStale
	}
	return r.transition(id, Cancelled, func(n *Registry, o *Operation) {
		n.slot(candidate.Slot).State, o.CandidateCleaned = Empty, true
	})
}

// ReuseCompleted records the fixed, historical resources actually consumed by
// the current preparation. It never clears the current original or candidate.
func (r Registry) ReuseCompleted(id, owner string, target Ref) (Registry, error) {
	return r.change(func(n *Registry) error {
		if n.Operation == nil || n.Operation.ID != id {
			return ErrStale
		}
		if n.Operation.Phase != Building && n.Operation.Phase != Cancelled {
			return ErrState
		}
		if target.Slot != n.Operation.Candidate.Slot || target.Revision == 0 || target.Revision >= n.Operation.Candidate.Revision {
			return ErrStale
		}
		for i := range n.History {
			o := &n.History[i]
			if o.ID != owner {
				continue
			}
			if o.Phase == Accepted && o.Original == target {
				o.OriginalCleaned = true
				return nil
			}
			if (o.Phase == Cancelled || o.Phase == RolledBack) && o.Candidate == target {
				o.CandidateCleaned = true
				return nil
			}
			return ErrStale
		}
		return ErrStale
	})
}

func (r Registry) SetMode(mode Mode) (Registry, error) {
	return r.change(func(n *Registry) error {
		if !validMode(mode) || n.Operation != nil && !terminal(n.Operation.Phase) {
			return ErrState
		}
		n.Mode = mode
		return nil
	})
}

// Begin reserves the inactive slot and freezes this operation's policy. Reusing
// that slot is disclosed/authorized in the caller's restore plan. No files are
// deleted here. Direct mode deliberately cannot use the parallel executor.
func (r Registry) Begin(id string, snapshot identity.SnapshotRef) (Registry, error) {
	return r.change(func(n *Registry) error {
		if n.Mode == Direct || !protection.ValidID(id) || !vault.ValidID(snapshot.VaultID) || !vault.ValidID(snapshot.SnapshotID) || n.Active == "" {
			return ErrState
		}
		if n.Operation != nil && (!terminal(n.Operation.Phase) || n.Operation.ID == id) || slices.ContainsFunc(n.History, func(o Operation) bool { return o.ID == id }) {
			return ErrState
		}
		target := B
		if n.Active == B {
			target = A
		}
		s := n.slot(target)
		if s.State != Empty && s.State != Stopped || s.Revision == ^uint64(0) {
			return ErrState
		}
		if n.Operation != nil {
			n.History = append(n.History, *n.Operation)
		}
		s.Revision++
		s.State = Preparing
		n.Operation = &Operation{ID: id, Snapshot: snapshot, Mode: n.Mode, Original: n.slot(n.Active).Ref, Candidate: s.Ref, Phase: Building}
		return nil
	})
}

func (r Registry) transition(id string, from Phase, fn func(*Registry, *Operation)) (Registry, error) {
	return r.change(func(n *Registry) error {
		if n.Operation == nil || n.Operation.ID != id {
			return ErrStale
		}
		if n.Operation.Phase != from {
			return ErrState
		}
		fn(n, n.Operation)
		return nil
	})
}

// Prepared records completed construction and an isolated gateway check, with
// all isolated candidate processes stopped. The original still runs.
func (r Registry) Prepared(id string) (Registry, error) {
	return r.transition(id, Building, func(n *Registry, o *Operation) {
		n.slot(o.Candidate.Slot).State, o.Phase = Ready, Prepared
	})
}

// OriginalStopped is evidence of a verified stop, never merely a stop exit code.
func (r Registry) OriginalStopped(id string) (Registry, error) {
	return r.transition(id, Stopping, func(n *Registry, o *Operation) {
		n.slot(o.Original.Slot).State, n.Active, o.Phase = Stopped, "", OriginalDown
	})
}

// StopOriginal journals the stop intent before touching the running service.
func (r Registry) StopOriginal(id string) (Registry, error) {
	return r.transition(id, Prepared, func(n *Registry, o *Operation) {
		n.slot(o.Original.Slot).State, n.Active, o.Phase = Unknown, "", Stopping
	})
}

// StartCandidate must be made durable BEFORE the start attempt. A disconnect
// leaves Unknown; it never authorizes restarting either environment blindly.
func (r Registry) StartCandidate(id string) (Registry, error) {
	return r.transition(id, OriginalDown, func(n *Registry, o *Operation) {
		n.slot(o.Candidate.Slot).State, o.Phase = Unknown, Starting
	})
}

func (r Registry) GatewayPassed(id string) (Registry, error) {
	return r.transition(id, Starting, func(n *Registry, o *Operation) {
		n.slot(o.Candidate.Slot).State, n.Active, o.Phase = Active, o.Candidate.Slot, AwaitingHuman
	})
}

// ConfirmUsable accepts only a separately authenticated human confirmation
// bound to this exact operation and incarnation. It does not authorize cleanup.
func (r Registry) ConfirmUsable(id string, candidate Ref) (Registry, error) {
	if r.Operation == nil || r.Operation.Candidate != candidate {
		return r, ErrStale
	}
	return r.transition(id, AwaitingHuman, func(_ *Registry, o *Operation) {
		o.Confirmed, o.Phase = true, Accepted
	})
}

func (r Registry) BeginRollback(id, failureCode string) (Registry, error) {
	return r.change(func(n *Registry) error {
		o := n.Operation
		if o == nil || o.ID != id {
			return ErrStale
		}
		acceptedReturn := o.Phase == Accepted && failureCode == "user_revert" && !o.OriginalCleaned
		if failureCode == "" || !acceptedReturn && !slices.Contains([]Phase{Building, Prepared, Stopping, OriginalDown, Starting, AwaitingHuman}, o.Phase) {
			return ErrState
		}
		n.slot(o.Candidate.Slot).State = Unknown
		if n.Active == o.Candidate.Slot {
			n.Active = ""
		}
		o.Phase, o.FailureCode = RollingBack, failureCode
		o.Confirmed = false // The candidate is no longer the accepted active environment.
		return nil
	})
}

// CandidateStopped is required even if a start attempt failed or timed out.
func (r Registry) CandidateStopped(id string) (Registry, error) {
	return r.transition(id, RollingBack, func(n *Registry, o *Operation) {
		n.slot(o.Candidate.Slot).State, o.Phase = Stopped, CandidateDown
	})
}

// OriginalHealthy records the original environment's actual health after
// resuming it (or verifying it stayed active after a preparation failure).
func (r Registry) OriginalHealthy(id string) (Registry, error) {
	return r.transition(id, Resuming, func(n *Registry, o *Operation) {
		n.slot(o.Original.Slot).State, n.Active, o.Phase = Active, o.Original.Slot, RolledBack
	})
}

// ResumeOriginal records the intent before starting/verifying the original.
func (r Registry) ResumeOriginal(id string) (Registry, error) {
	return r.transition(id, CandidateDown, func(n *Registry, o *Operation) {
		n.slot(o.Original.Slot).State, n.Active, o.Phase = Unknown, "", Resuming
	})
}

// BeginCleanup records a separate explicit user command before deleting files.
// Compact's frozen policy or an explicitly saved direct policy permits a
// fresh post-acceptance deletion review. Saving policy alone deletes nothing.
func (r Registry) BeginCleanup(id string, original Ref) (Registry, error) {
	if r.Operation == nil || r.Operation.Original != original {
		return r, ErrStale
	}
	if r.Operation.Mode != Compact && r.Mode != Direct || r.Operation.OriginalCleaned {
		return r, ErrState
	}
	return r.transition(id, Accepted, func(_ *Registry, o *Operation) { o.Phase = Cleaning })
}

func (r Registry) CleanupCompleted(id string) (Registry, error) {
	return r.transition(id, Cleaning, func(n *Registry, o *Operation) {
		s := n.slot(o.Original.Slot)
		s.State = Empty
		o.OriginalCleaned, o.Phase = true, Accepted
	})
}

func (r Registry) CleanupFailed(id string) (Registry, error) {
	return r.transition(id, Cleaning, func(_ *Registry, o *Operation) { o.Phase = Accepted })
}
