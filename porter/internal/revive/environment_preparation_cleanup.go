package revive

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"valise/core/environment"
	"valise/core/identity"
	"valise/core/revive"
	porterenroll "valise/porter/internal/enroll"
	"valise/porter/internal/environments"
	"valise/porter/internal/managedruntime"
	"valise/porter/internal/seal"
)

type PreparationCleanupItem struct {
	Kind        string             `json:"kind"`
	Root        string             `json:"root,omitempty"`
	Present     bool               `json:"present"`
	EmptyOnly   bool               `json:"empty_only"`
	Identity    revive.ManagedRoot `json:"identity"`
	DisplayKind string             `json:"display_kind,omitempty"`
	Tree        string             `json:"tree,omitempty"`
}

type PreparationCleanupReview struct {
	Agent            identity.AgentRef        `json:"agent"`
	OperationID      string                   `json:"operation_id"`
	Snapshot         identity.SnapshotRef     `json:"snapshot"`
	State            string                   `json:"state"`
	Native           managedruntime.NativeRef `json:"native"`
	RegistryRevision uint64                   `json:"registry_revision"`
	Items            []PreparationCleanupItem `json:"items"`
	Digest           string                   `json:"digest"`
}

// ReviewEnvironmentPreparationCleanup reads a fixed, already cancelled scope.
// It does not authorize deletion or turn an unknown probe into success.
func (r *Runner) ReviewEnvironmentPreparationCleanup(ctx context.Context, agent identity.AgentRef, op string) (*PreparationCleanupReview, error) {
	v, _, leases, err := r.reviewPreparationCleanup(ctx, agent, op, managedruntime.Controller{Home: r.Store.Home})
	closePreparationLeases(leases)
	return v, err
}

func (r *Runner) reviewPreparationCleanup(ctx context.Context, agent identity.AgentRef, op string, control managedruntime.Controller) (*PreparationCleanupReview, *EnvironmentPreparation, []*os.File, error) {
	p, err := loadPreparation(r.Store.Home, agent, op)
	if err != nil {
		return nil, nil, nil, err
	}
	if p.State == "cleaned" {
		v := &PreparationCleanupReview{Agent: agent, OperationID: op, State: p.State, Snapshot: identity.SnapshotRef{VaultID: p.Params.Input.VaultID, SnapshotID: p.Plan.SnapshotID}}
		v.Digest = environments.Digest(p)
		return v, p, nil, nil
	}
	if p.State != "cancelled" && p.State != "cleaning" {
		return nil, p, nil, preparationCleanupError("environment_prepare_cleanup_changed")
	}
	native, registry, err := r.preparationUnchanged(ctx, p, control)
	if err != nil {
		return nil, p, nil, err
	}
	if registry.Operation != nil && registry.Operation.ID == op && registry.Operation.Phase != environment.Cancelled {
		return nil, p, nil, preparationCleanupError("environment_prepare_cleanup_changed")
	}
	leases, err := holdPreparationProbe(r.Store.Home, p)
	if err != nil {
		return nil, p, nil, err
	}
	priorLeases, err := r.holdReuseProbes(p)
	if err != nil {
		closePreparationLeases(leases)
		return nil, p, nil, err
	}
	leases = append(leases, priorLeases...)
	fail := func(err error) (*PreparationCleanupReview, *EnvironmentPreparation, []*os.File, error) {
		closePreparationLeases(leases)
		return nil, p, nil, err
	}
	items, err := r.preparationCleanupItemsContext(ctx, p)
	if err != nil {
		return fail(err)
	}
	settings, err := (environments.Store{Home: r.Store.Home}).Settings(agent)
	if err != nil {
		return fail(err)
	}
	v := &PreparationCleanupReview{Agent: agent, OperationID: op, State: p.State, Snapshot: identity.SnapshotRef{VaultID: p.Params.Input.VaultID, SnapshotID: p.Plan.SnapshotID}, Native: native, RegistryRevision: registry.Revision, Items: items}
	v.Digest = environments.Digest(struct {
		Record   *EnvironmentPreparation
		View     *PreparationCleanupReview
		Settings string
	}{p, v, settings.Digest})
	return v, p, leases, nil
}

func (r *Runner) preparationCleanupItems(p *EnvironmentPreparation) ([]PreparationCleanupItem, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	return r.preparationCleanupItemsContext(ctx, p)
}

func (r *Runner) preparationCleanupItemsContext(ctx context.Context, p *EnvironmentPreparation) ([]PreparationCleanupItem, error) {
	var items []PreparationCleanupItem
	add := func(kind, root string) error {
		path, err := preparationResource(r.Store.Home, p, kind, root)
		if err != nil {
			return err
		}
		item := PreparationCleanupItem{Kind: kind, Root: root, EmptyOnly: true}
		fact, err := managedRootIdentity(kind+":"+root, path)
		if errors.Is(err, os.ErrNotExist) {
			items = append(items, item)
			return nil
		}
		if err != nil {
			return preparationCleanupError("environment_prepare_cleanup_unknown")
		}
		item.Present, item.Identity = true, fact
		for _, claim := range p.Claims {
			if claim.Kind == kind && claim.Root == root {
				if claim.Removed || claim.Identity != fact {
					return preparationCleanupError("environment_prepare_cleanup_unknown")
				}
				item.EmptyOnly = false
			}
		}
		if item.EmptyOnly {
			entries, err := os.ReadDir(path)
			if err != nil || len(entries) != 0 {
				return preparationCleanupError("environment_prepare_cleanup_unknown")
			}
		}
		// Fixed sibling/private names are never allowed to intersect an accepted
		// live root, including a different Agent or a newly enrolled instance.
		records, err := acceptedCleanupRoots(r.Store.Home)
		if err != nil {
			return err
		}
		for _, live := range records {
			if within(live, path) || within(path, live) {
				return preparationCleanupError("environment_prepare_cleanup_unknown")
			}
		}
		items = append(items, item)
		return nil
	}
	if p.ManagedSource == nil {
		if err := add("program-original", ""); err != nil {
			return nil, err
		}
	}
	if p.Candidate.Revision != 0 {
		if err := add("program-candidate", ""); err != nil {
			return nil, err
		}
	}
	for _, root := range p.Plan.Roots {
		if root.SwapRoot == "" {
			if err := add("stage", root.ID); err != nil {
				return nil, err
			}
		}
	}
	if err := add("workspace", ""); err != nil {
		return nil, err
	}
	if p.Reuse != nil {
		for index := range p.Reuse.Items {
			item, err := r.preparationReuseCleanupItem(ctx, p, index)
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		}
	}
	return items, nil
}

// CleanupEnvironmentPreparation is a separate explicit management action. The
// expected review binds the human-visible scope; it is not a general ticket.
func (r *Runner) CleanupEnvironmentPreparation(ctx context.Context, agent identity.AgentRef, op, expected string) (*EnvironmentPreparation, error) {
	return r.cleanupEnvironmentPreparation(ctx, agent, op, expected, managedruntime.Controller{Home: r.Store.Home})
}
func (r *Runner) cleanupEnvironmentPreparation(ctx context.Context, agent identity.AgentRef, op, expected string, control managedruntime.Controller) (*EnvironmentPreparation, error) {
	if expected == "" {
		return nil, preparationCleanupError("environment_prepare_cleanup_changed")
	}
	unlock, err := (seal.Paths{Home: r.Store.Home}).LockFor("environment_cleanup_preparation")
	if err != nil {
		return nil, err
	}
	defer unlock()
	view, p, leases, err := r.reviewPreparationCleanup(ctx, agent, op, control)
	if err != nil {
		return p, err
	}
	defer closePreparationLeases(leases)
	if p.State == "cleaned" {
		return p, nil
	}
	if expected != view.Digest && (p.State != "cleaning" || expected != p.CleanupReview) {
		return p, preparationCleanupError("environment_prepare_cleanup_changed")
	}
	if p.State != "cleaning" {
		p.State, p.CleanupReview = "cleaning", view.Digest
		if err := savePreparation(r.Store.Home, p, false); err != nil {
			return p, err
		}
	}
	for _, item := range view.Items {
		if err := ctx.Err(); err != nil {
			return p, err
		}
		path, err := preparationResource(r.Store.Home, p, item.Kind, item.Root)
		if err != nil {
			return p, err
		}
		if item.Present {
			if item.Kind == "reuse" {
				if err := r.beginReuseCleanupItem(ctx, p, item.Root, item.Tree); err != nil {
					return p, err
				}
			}
			fact, err := managedRootIdentity(item.Kind+":"+item.Root, path)
			if err != nil || fact != item.Identity {
				return p, preparationCleanupError("environment_prepare_cleanup_unknown")
			}
			if item.EmptyOnly {
				err = os.Remove(path)
			} else {
				err = os.RemoveAll(path)
			}
			if err != nil {
				return p, err
			}
			if err := syncDataDirs(filepath.Dir(path)); err != nil {
				return p, err
			}
		}
		for i := range p.Claims {
			if p.Claims[i].Kind == item.Kind && p.Claims[i].Root == item.Root {
				p.Claims[i].Removed = true
			}
		}
		if item.Kind == "reuse" {
			if err := r.completeReuseCleanupItem(p, item.Root); err != nil {
				return p, err
			}
		}
		if err := savePreparation(r.Store.Home, p, false); err != nil {
			return p, err
		}
	}
	if p.Reuse != nil {
		p.Reuse.Complete = true
		if err := savePreparation(r.Store.Home, p, false); err != nil {
			return p, err
		}
		if err := r.finishReuseMetadata(p); err != nil {
			return p, err
		}
	}
	store := environments.Store{Home: r.Store.Home}
	registry, err := store.Load(agent)
	if err == nil && p.ManagedSource == nil {
		err = store.ReleaseInitial(agent, registry.Revision, op)
	} else if err == nil && registry.Operation != nil && registry.Operation.ID == op {
		_, err = store.Update(agent, registry.Revision, func(v environment.Registry) (environment.Registry, error) {
			return v.PreparationCleaned(op, p.Candidate)
		})
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return p, err
	}
	p.State = "cleaned"
	if err := savePreparation(r.Store.Home, p, false); err != nil {
		return p, err
	}
	return p, nil
}

// Retired registrations also retain user data. They cannot be deleted as a
// side effect of clearing a different operation's preparation directory.
func acceptedCleanupRoots(home string) ([]string, error) {
	records, err := porterenroll.ListRecords(home)
	if err != nil {
		return nil, err
	}
	var roots []string
	for _, record := range records {
		for _, path := range record.Roots {
			roots = append(roots, path)
		}
	}
	return roots, nil
}
