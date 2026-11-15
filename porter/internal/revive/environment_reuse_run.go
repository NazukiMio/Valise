package revive

import (
	"context"
	"os"
	"path/filepath"

	"valise/core/environment"
	"valise/porter/internal/activationrecord"
	"valise/porter/internal/environments"
	"valise/porter/internal/managedruntime"
)

func (r *Runner) holdReuseProbes(p *EnvironmentPreparation) ([]*os.File, error) {
	if p.Reuse == nil {
		return nil, nil
	}
	var leases []*os.File
	for _, owner := range p.Reuse.Owners {
		old, err := loadPreparation(r.Store.Home, p.Params.Agent, owner.Operation)
		if err != nil {
			closePreparationLeases(leases)
			return nil, err
		}
		deletedWorkspace := false
		for _, item := range p.Reuse.Items {
			if item.Owner == owner.Operation && item.Kind == "workspace" && item.Intent && owner.ProbeStopped {
				path, e := reuseItemPath(r.Store.Home, environment.PreparationParams{Agent: p.Params.Agent}, item)
				if e != nil {
					closePreparationLeases(leases)
					return nil, e
				}
				if _, e = os.Lstat(filepath.Join(path, old.ProbeWorkspace, "control/lifetime.lock")); os.IsNotExist(e) {
					deletedWorkspace = true
				}
			}
		}
		if deletedWorkspace {
			continue
		}
		files, err := holdPreparationProbe(r.Store.Home, old)
		if err != nil {
			closePreparationLeases(leases)
			return nil, err
		}
		leases = append(leases, files...)
	}
	return leases, nil
}

func (r *Runner) executePreparationReuse(ctx context.Context, p *EnvironmentPreparation, control managedruntime.Controller) error {
	if p.Reuse == nil {
		return nil
	}
	if p.Reuse.Complete {
		return r.finishReuseMetadata(p)
	}
	leases, err := r.holdReuseProbes(p)
	if err != nil {
		return err
	}
	defer closePreparationLeases(leases)
	for index := range p.Reuse.Items {
		item := &p.Reuse.Items[index]
		path, err := reuseItemPath(r.Store.Home, environment.PreparationParams{Agent: p.Params.Agent}, *item)
		if err != nil {
			return err
		}
		if err := r.protectReuse(ctx, p, *item, path); err != nil {
			return err
		}
		if _, err := r.managedPreparationUnchanged(ctx, p, control, false); err != nil {
			return err
		}
		present, err := inspectReuse(path, *item)
		if err != nil {
			return err
		}
		if present {
			item.Intent = true
			if err := savePreparation(r.Store.Home, p, false); err != nil {
				return err
			}
			if err := r.protectReuse(ctx, p, *item, path); err != nil {
				return err
			}
			if still, err := inspectReuse(path, *item); err != nil || !still {
				return reuseError()
			}
			if tree, _, _, err := oldCleanupTree(ctx, path, item.Kind != "workspace"); err != nil || tree != item.Tree {
				return reuseError()
			}
			if err := os.RemoveAll(path); err != nil {
				return err
			}
			if err := syncDataDirs(filepath.Dir(path)); err != nil {
				return err
			}
		}
		item.Intent, item.Removed = true, true
		if err := savePreparation(r.Store.Home, p, false); err != nil {
			return err
		}
	}
	p.Reuse.Complete = true
	if err := savePreparation(r.Store.Home, p, false); err != nil {
		return err
	}
	return r.finishReuseMetadata(p)
}

func (r *Runner) finishReuseMetadata(p *EnvironmentPreparation) error {
	if p.Reuse == nil || !p.Reuse.Complete {
		return nil
	}
	store := environments.Store{Home: r.Store.Home}
	registry, err := store.Load(p.Params.Agent)
	if err != nil {
		return err
	}
	if registry.Operation == nil || registry.Operation.ID != p.OperationID {
		return reuseError()
	}
	for _, owner := range p.Reuse.Owners {
		old, err := loadPreparation(r.Store.Home, p.Params.Agent, owner.Operation)
		if err != nil {
			return err
		}
		if owner.Accepted {
			parent, err := loadActivation(r.Store.Home, p.Params.Agent, owner.Operation)
			if err != nil {
				return err
			}
			j, err := r.Store.Load(parent.Plan.DataOpID)
			if err != nil {
				return err
			}
			a := NewBoundActivationRunner(r)
			items, err := a.oldCleanupClaims(parent, registry, j)
			if err != nil {
				return err
			}
			for i := range items {
				items[i].Intent, items[i].Removed = true, true
			}
			parent.Cleanup = &activationrecord.Cleanup{Target: owner.Target, ProbeStopped: true, Complete: true, Items: items}
			if err := saveActivation(r.Store.Home, parent); err != nil {
				return err
			}
		} else {
			for i := range old.Claims {
				claim := &old.Claims[i]
				if claim.Kind != "program-original" {
					claim.Removed = true
				}
			}
			if old.Reuse != nil {
				for i := range old.Reuse.Items {
					old.Reuse.Items[i].Intent, old.Reuse.Items[i].Removed = true, true
				}
				old.Reuse.Complete = true
			}
			old.State = "cleaned"
			if err := savePreparation(r.Store.Home, old, false); err != nil {
				return err
			}
		}
		known := false
		for _, past := range registry.History {
			if past.ID == owner.Operation {
				known = true
			}
		}
		if known {
			registry, err = store.Update(p.Params.Agent, registry.Revision, func(v environment.Registry) (environment.Registry, error) {
				return v.ReuseCompleted(p.OperationID, owner.Operation, owner.Target)
			})
			if err != nil {
				return err
			}
		}
	}
	return nil
}
