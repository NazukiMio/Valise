package backuprun

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"valise/core/identity"
	cp "valise/core/protection"
	"valise/core/stationsync"
	"valise/core/vault"
	"valise/core/vaultrpc"
	"valise/porter/internal/fault"
	"valise/porter/internal/seal"
)

// cancelPoll is the interval for checking cancel requests during an active copy.
var cancelPoll = 200 * time.Millisecond

func (s Store) persist(r *Run) error { r.UpdatedAt = time.Now().UTC(); return s.save(r) }

// execute runs the claimed record. Cancel requests are honoured at safe points
// (before sealing starts and before each copy target) and by interrupting an
// active copy transfer.
func (s Store) execute(ctx context.Context, r *Run) error {
	if s.stop(r, cp.StageSealing) {
		s.markCancelled(r)
		return s.persist(r)
	}
	r.State = "running"
	r.Stage = cp.StageSealing
	r.Components[0].State = "running"
	started := time.Now().UTC()
	r.Components[0].StartedAt = &started
	if err := s.persist(r); err != nil {
		return err
	}
	fault.Hit(s.Home, "backup-after-running")
	paths := seal.Paths{Home: s.Home}
	tier := r.Plan.Scope.Tier
	if r.Request.Selection.Tier != "" {
		tier = r.Request.Selection.Tier
	}
	initiator := "planned"
	if r.Request.Reason == "timer" {
		initiator = vaultrpc.InitiatorTimer
	}
	rep, err := seal.SealPlanned(ctx, s.Env, paths, vaultrpc.SealParams{InstanceID: r.AgentRef.InstanceID, Tier: tier,
		Reason: r.Request.Reason, Note: r.Request.Selection.Note}, initiator)
	if err != nil {
		// A failed call can have crossed the snapshot publication boundary. Never
		// retry it or claim no snapshot exists without fixed-executor evidence.
		r.State = "reconciling"
		r.ErrorCode = "result_unknown"
		r.Components[0].State = "unknown"
		r.Components[0].ErrorCode = "backup_failed"
		r.Stage = ""
		return s.persist(r)
	}
	dir, err := paths.LeafDir(rep.Leaf)
	var desc vault.Descriptor
	if err == nil {
		var b []byte
		b, err = os.ReadFile(filepath.Join(dir, vault.DescriptorFile))
		if err == nil {
			err = json.Unmarshal(b, &desc)
		}
	}
	if err != nil || desc.Format != vault.FormatVault || desc.VaultID == "" || rep.SnapshotID == "" {
		r.State = "reconciling"
		r.ErrorCode = "result_unknown"
		r.Components[0].State = "unknown"
		r.Stage = ""
		return s.persist(r)
	}
	r.Snapshot = &identity.SnapshotRef{VaultID: desc.VaultID, SnapshotID: rep.SnapshotID}
	now := time.Now().UTC()
	r.Components[0].State = "succeeded"
	r.Components[0].CompletedAt = &now
	// Persist the AgentRef -> SnapshotRef binding before starting any copy.
	if err := s.persist(r); err != nil {
		return err
	}
	partial, cancelled := false, false
	for i, copy := range r.Plan.ServerCopies {
		if r.Request.Selection.SkipCopies {
			copy.Enabled = new(false)
		}
		ref := copy.TargetRef
		component := cp.BackupComponent{Kind: "server_copy", TargetRef: &ref, State: "skipped"}
		if !cancelled && copy.Enabled != nil && *copy.Enabled && s.stop(r, cp.StageCopying) {
			// The committed snapshot stays bound; copies not started are
			// recorded as skipped by the cancel request.
			cancelled = true
		}
		if cancelled {
			if copy.Enabled != nil && *copy.Enabled {
				component.ErrorCode = "cancelled"
			}
		} else if copy.Enabled != nil && *copy.Enabled {
			due, checkErr := s.copyDue(r, copy)
			switch {
			case ref.AuthorityID != r.AgentRef.ServerUID:
				component.State = "failed"
				component.ErrorCode = "identity_conflict"
				partial = true
			case checkErr != nil:
				component.State = "unknown"
				component.ErrorCode = "record_corrupt"
				partial = true
			case due:
				component.State = "running"
				at := time.Now().UTC()
				component.StartedAt = &at
				r.Stage = cp.StageCopying
				r.StageTarget = &ref
				r.Components = append(r.Components, component)
				if err := s.persist(r); err != nil {
					return err
				}
				copyErr, copyCancelled := s.runCopy(ctx, r.RunID, paths, ref.ID)
				completed := time.Now().UTC()
				component.CompletedAt = &completed
				if copyCancelled {
					component.State = "cancelled"
					component.ErrorCode = "cancelled"
					r.Components[len(r.Components)-1] = component
					s.appendSkippedRemaining(r, r.Plan.ServerCopies[i+1:])
					s.markCancelled(r)
					return s.persist(r)
				}
				component.State = "succeeded"
				if copyErr != nil {
					component.State = "failed"
					component.ErrorCode = "copy_failed"
					component.FailureCause = stationsync.CauseOf(copyErr)
					partial = true
				}
				r.Components[len(r.Components)-1] = component
				if err := s.persist(r); err != nil {
					return err
				}
				continue
			}
		}
		r.Components = append(r.Components, component)
		if err := s.persist(r); err != nil {
			return err
		}
	}
	if !cancelled && ctx.Err() == nil && initiator == vaultrpc.InitiatorTimer && r.Plan.Period.KeepAutoPeriods > 0 {
		component := cp.BackupComponent{Kind: "leaf_retire", State: "succeeded"}
		if err := seal.RetireExpiredAutoLeaves(paths, r.AgentRef.InstanceID, r.Plan.Period.KeepAutoPeriods); err != nil {
			component.State, component.ErrorCode = "failed", "leaf_retire_failed"
			partial = true
		}
		r.Components = append(r.Components, component)
	}
	r.Stage = ""
	r.StageTarget = nil
	switch {
	case cancelled:
		s.markCancelled(r)
	case partial:
		r.State = "partial"
	default:
		r.State = "succeeded"
	}
	return s.persist(r)
}

func (s Store) syncCopy(ctx context.Context, paths seal.Paths, targetID string) (*vaultrpc.SyncResult, error) {
	if s.syncFn != nil {
		return s.syncFn(ctx, s.Env, paths, targetID, "planned")
	}
	return seal.Sync(ctx, s.Env, paths, targetID, "planned")
}

// runCopy runs one due target copy while polling the run's cancel marker file
// by runID (without touching r concurrently) so an unresponsive target exits promptly.
func (s Store) runCopy(ctx context.Context, runID string, paths seal.Paths, targetID string) (copyErr error, cancelled bool) {
	if s.cancelRequested(runID) != nil {
		return context.Canceled, true
	}
	copyCtx, cancelCopy := context.WithCancel(ctx)
	defer cancelCopy()

	done := make(chan struct{})
	var cancelTriggered atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(cancelPoll)
		defer ticker.Stop()
		for {
			if s.cancelRequested(runID) != nil {
				cancelTriggered.Store(true)
				cancelCopy()
				return
			}
			select {
			case <-done:
				return
			case <-ctx.Done():
				if s.cancelRequested(runID) != nil {
					cancelTriggered.Store(true)
				}
				cancelCopy()
				return
			case <-ticker.C:
			}
		}
	}()

	_, err := s.syncCopy(copyCtx, paths, targetID)
	close(done)
	wg.Wait()
	return err, err != nil && (cancelTriggered.Load() || s.cancelRequested(runID) != nil)
}

func (s Store) appendSkippedRemaining(r *Run, rest []cp.ServerCopy) {
	for _, rem := range rest {
		if r.Request.Selection.SkipCopies {
			rem.Enabled = new(false)
		}
		ref := rem.TargetRef
		component := cp.BackupComponent{Kind: "server_copy", TargetRef: &ref, State: "skipped"}
		if rem.Enabled != nil && *rem.Enabled {
			component.ErrorCode = "cancelled"
		}
		r.Components = append(r.Components, component)
	}
}

// copyDue uses only recorded successful copies for this Agent and target. A
// different generation or another legacy instance cannot throttle this plan.
func (s Store) copyDue(current *Run, c cp.ServerCopy) (bool, error) {
	interval, err := time.ParseDuration(c.MinimumInterval)
	if err != nil {
		return false, err
	}
	entries, err := os.ReadDir(s.dir())
	if err != nil {
		return false, err
	}
	latest := time.Time{}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		id := e.Name()[:len(e.Name())-5]
		if !cp.ValidID(id) {
			return false, failure("record_corrupt")
		}
		if id == current.RunID {
			continue
		}
		r, err := s.load(id)
		if err != nil {
			return false, err
		}
		if r.AgentRef != current.AgentRef {
			continue
		}
		for _, v := range r.Components {
			if v.Kind == "server_copy" && v.TargetRef != nil && *v.TargetRef == c.TargetRef && v.State == "succeeded" && v.CompletedAt != nil && v.CompletedAt.After(latest) {
				latest = *v.CompletedAt
			}
		}
	}
	return latest.IsZero() || time.Since(latest) >= interval, nil
}
