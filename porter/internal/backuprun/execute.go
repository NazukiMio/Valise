package backuprun

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"valise/core/identity"
	cp "valise/core/protection"
	"valise/core/vault"
	"valise/core/vaultrpc"
	"valise/porter/internal/seal"
)

func (s Store) persist(r *Run) error { r.UpdatedAt = time.Now().UTC(); return s.save(r) }

// execute runs the claimed record. Cancel requests are honoured only at the
// safe points: before sealing starts and before each copy target.
func (s Store) execute(ctx context.Context, r *Run) error {
	if s.stop(r, cp.StageSealing) {
		s.markCancelled(r)
		return s.persist(r)
	}
	r.State = "running"
	r.Stage = cp.StageSealing
	r.Components[0].State = "running"
	if err := s.persist(r); err != nil {
		return err
	}
	paths := seal.Paths{Home: s.Home}
	rep, err := seal.SealPlanned(ctx, s.Env, paths, vaultrpc.SealParams{InstanceID: r.AgentRef.InstanceID, Tier: r.Plan.Scope.Tier, Reason: r.Request.Reason}, "planned")
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
	for _, copy := range r.Plan.ServerCopies {
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
				r.Stage = cp.StageCopying
				r.StageTarget = &ref
				r.Components = append(r.Components, component)
				if err := s.persist(r); err != nil {
					return err
				}
				_, copyErr := seal.Sync(ctx, s.Env, paths, ref.ID, "planned")
				component.State = "succeeded"
				if copyErr != nil {
					component.State = "failed"
					component.ErrorCode = "copy_failed"
					partial = true
				} else {
					at := time.Now().UTC()
					component.CompletedAt = &at
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
