package revive

import (
	"context"
	"os"
	"path/filepath"

	"valise/core/environment"
	"valise/core/identity"
	"valise/porter/internal/activationrecord"
	"valise/porter/internal/activeview"
	"valise/porter/internal/seal"
)

// CleanupOldReviewed is a separate explicit human command. A partially removed
// directory is never treated as an unused slot or resumed by a status query.
func (a *ActivationRunner) CleanupOldReviewed(ctx context.Context, agent identity.AgentRef, op, expected string) (*environment.SwitchStatus, error) {
	if ctx == nil {
		return nil, oldCleanupError("environment_cleanup_unknown")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if expected == "" {
		return nil, oldCleanupError("environment_cleanup_changed")
	}
	previous, err := loadActivation(a.home(), agent, op)
	if err != nil {
		return nil, err
	}
	known, err := a.registry(previous)
	if err != nil {
		return nil, err
	}
	if known.Operation.OriginalCleaned {
		return a.SwitchStatus(ctx, agent, op)
	}
	unlock, err := (seal.Paths{Home: a.home()}).LockFor("environment_cleanup_old")
	if err != nil {
		return nil, err
	}
	defer unlock()
	s, err := a.reviewOldCleanupLocked(ctx, agent, op)
	if err != nil {
		return nil, err
	}
	defer closePreparationLeases(s.Leases)
	if s.View.State == "cleaned" {
		return a.SwitchStatus(ctx, agent, op)
	}
	if s.View.Digest != expected {
		return nil, oldCleanupError("environment_cleanup_changed")
	}
	rec := s.Record
	if s.Registry.Operation.Phase != environment.Cleaning {
		if err := a.transition(rec, func(r environment.Registry, id string) (environment.Registry, error) {
			return r.BeginCleanup(id, rec.Plan.Original)
		}); err != nil {
			return nil, err
		}
	}
	if rec.Cleanup == nil {
		rec.Cleanup = &activationrecord.Cleanup{Target: rec.Plan.Original, ProbeStopped: true}
		for _, resource := range s.Resources {
			rec.Cleanup.Items = append(rec.Cleanup.Items, resource.Item)
		}
		if err := saveActivation(a.home(), rec); err != nil {
			return nil, err
		}
	}
	fail := func(code string) (*environment.SwitchStatus, error) {
		rec.Cleanup.IssueCode = code
		if err := saveActivation(a.home(), rec); err != nil {
			return nil, oldCleanupError("environment_cleanup_io_failed")
		}
		return nil, oldCleanupError(code)
	}
	for i := range rec.Cleanup.Items {
		if err := ctx.Err(); err != nil {
			return fail("environment_cleanup_interrupted")
		}
		item := &rec.Cleanup.Items[i]
		path, err := oldCleanupPath(a.home(), rec, s.Child, *item)
		if err != nil {
			return fail("environment_cleanup_io_failed")
		}
		if err := a.protectOldCleanup(ctx, rec, path, item.Kind); err != nil {
			return fail("environment_cleanup_io_failed")
		}
		current, err := (activeview.Reader{Home: a.home(), Control: a.runtime}).Read(ctx, agent)
		if err != nil || current == nil || current.Ref != rec.Plan.Candidate || !current.ConversationConfirmed || current.Definition.Digest() != rec.CandidateDefinition || (!current.Current.Running && !current.Current.Stopped) {
			return fail("environment_cleanup_io_failed")
		}
		present, err := inspectOldCleanupIdentity(path, *item)
		if err != nil {
			return fail("environment_cleanup_io_failed")
		}
		if present {
			item.Intent = true
			if err := saveActivation(a.home(), rec); err != nil {
				return nil, oldCleanupError("environment_cleanup_io_failed")
			}
			if err := a.protectOldCleanup(ctx, rec, path, item.Kind); err != nil {
				return fail("environment_cleanup_io_failed")
			}
			if still, err := inspectOldCleanupIdentity(path, *item); err != nil || !still {
				return fail("environment_cleanup_io_failed")
			}
			if err := os.RemoveAll(path); err != nil {
				return fail("environment_cleanup_io_failed")
			}
			if err := syncDataDirs(filepath.Dir(path)); err != nil {
				return fail("environment_cleanup_io_failed")
			}
		}
		item.Intent, item.Removed = true, true
		if err := saveActivation(a.home(), rec); err != nil {
			return nil, oldCleanupError("environment_cleanup_io_failed")
		}
	}
	rec.Cleanup.Complete, rec.Cleanup.IssueCode = true, ""
	if err := saveActivation(a.home(), rec); err != nil {
		return nil, oldCleanupError("environment_cleanup_io_failed")
	}
	if err := a.transition(rec, environment.Registry.CleanupCompleted); err != nil {
		return nil, err
	}
	return a.SwitchStatus(ctx, agent, op)
}
