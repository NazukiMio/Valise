package revive

import (
	"context"
	"os"
	"slices"
	"sort"
	"strings"

	"valise/core/environment"
	"valise/core/protection"
	"valise/core/revive"
	"valise/porter/internal/environments"
	"valise/porter/internal/managedruntime"
)

func validPreparationReuse(p *EnvironmentPreparation) bool {
	u := p.Reuse
	if u == nil {
		return true
	}
	if p.ManagedSource == nil || u.Target.Slot == p.Original.Slot || u.Target.Slot != p.Candidate.Slot || u.Target.Revision == ^uint64(0) || u.Target.Revision+1 != p.Candidate.Revision || len(u.Items) > 100000 || len(u.Owners) > 10000 {
		return false
	}
	owners := map[string]bool{}
	for _, owner := range u.Owners {
		if owners[owner.Operation] || !protection.ValidID(owner.Operation) || owner.Operation == p.OperationID || !owner.ProbeStopped || owner.Target.Slot != u.Target.Slot || owner.Target.Revision == 0 {
			return false
		}
		owners[owner.Operation] = true
	}
	seen := map[string]bool{}
	for _, item := range u.Items {
		if !owners[item.Owner] || seen[item.key()] || item.Identity.ID != item.key() || item.Identity.Inode == 0 || len(item.Tree) != 64 || item.Removed && !item.Intent || u.Complete && !item.Removed {
			return false
		}
		seen[item.key()] = true
		switch item.Kind {
		case "old-data", "failed-data", "stage":
			if item.Root == "" || strings.ContainsAny(item.Root, "/\\\x00") || item.Root == "." || item.Root == ".." {
				return false
			}
		case "program-original", "program-candidate", "workspace":
			if item.Root != "" {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func reuseIntentDigest(u *preparationReuse) string {
	copy := *u
	copy.Complete = false
	copy.Items = slices.Clone(u.Items)
	for i := range copy.Items {
		copy.Items[i].Intent, copy.Items[i].Removed = false, false
	}
	return environments.Digest(copy)
}

func (r *Runner) inspectInactiveReuse(ctx context.Context, registry environment.Registry, current environment.Ref, control managedruntime.Controller) (*preparationReuse, []environment.OldCleanupItem, string, error) {
	index := 0
	if current.Slot == environment.A {
		index = 1
	}
	target := registry.Slots[index]
	if target.State != environment.Empty && target.State != environment.Stopped {
		return nil, nil, "", reuseError()
	}
	u := &preparationReuse{Target: target.Ref}
	items := map[string]reuseItem{}
	owners := map[string]reuseOwner{}
	addOwner := func(o reuseOwner) error {
		if old, ok := owners[o.Operation]; ok && old != o {
			return reuseError()
		}
		owners[o.Operation] = o
		return nil
	}
	add := func(item reuseItem) error {
		item.Identity.ID = item.key()
		if old, ok := items[item.key()]; ok {
			left, right := old, item
			left.Intent, left.Removed, left.Tree = false, false, ""
			right.Intent, right.Removed, right.Tree = false, false, ""
			if left != right {
				return reuseError()
			}
			item.Intent, item.Removed = item.Intent || old.Intent, item.Removed || old.Removed
			if item.Tree == "" {
				item.Tree = old.Tree
			}
		}
		items[item.key()] = item
		return nil
	}
	old := registry.Operation
	if old != nil && old.Phase == environment.Accepted && !old.OriginalCleaned {
		if old.Original != target.Ref || old.Candidate != current || !old.Confirmed {
			return nil, nil, "", reuseError()
		}
		a := NewBoundActivationRunner(r)
		a.runtime = control
		parent, err := loadActivation(r.Store.Home, registry.Agent, old.ID)
		if err != nil {
			return nil, nil, "", err
		}
		j, err := r.Store.Load(parent.Plan.DataOpID)
		if err != nil {
			return nil, nil, "", err
		}
		claims, err := a.oldCleanupClaims(parent, registry, j)
		if err != nil {
			return nil, nil, "", err
		}
		leases, err := a.oldCleanupLeases(parent)
		if err != nil {
			return nil, nil, "", err
		}
		closePreparationLeases(leases)
		if err := addOwner(reuseOwner{Operation: old.ID, Target: old.Original, Accepted: true, ProbeStopped: true}); err != nil {
			return nil, nil, "", err
		}
		for _, claim := range claims {
			kind := "workspace"
			if claim.Kind == "program" {
				kind = "program-original"
			}
			if claim.Kind == "data" {
				kind = "old-data"
			}
			if err := add(reuseItem{Owner: old.ID, Kind: kind, Root: claim.Root, Identity: claim.Identity}); err != nil {
				return nil, nil, "", err
			}
		}
	}
	dir, err := preparationDir(r.Store.Home, registry.Agent, false)
	if err != nil {
		return nil, nil, "", err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, "", err
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		p, err := loadPreparation(r.Store.Home, registry.Agent, strings.TrimSuffix(entry.Name(), ".json"))
		if err != nil {
			return nil, nil, "", err
		}
		if p.State == "cleaned" || p.Candidate.Slot != target.Slot {
			continue
		}
		var operation *environment.Operation
		if old != nil && old.ID == p.OperationID {
			operation = old
		}
		for i := range registry.History {
			if registry.History[i].ID == p.OperationID {
				operation = &registry.History[i]
			}
		}
		eligible := p.State == "cancelled" || p.State == "failed" && operation == nil || operation != nil && operation.Phase == environment.RolledBack
		if !eligible {
			continue
		}
		if p.ManagedSource == nil && (operation == nil || operation.Phase != environment.RolledBack) {
			return nil, nil, "", reuseError()
		}
		if operation != nil && operation.Phase != environment.Cancelled && operation.Phase != environment.RolledBack {
			return nil, nil, "", reuseError()
		}
		leases, err := holdPreparationProbe(r.Store.Home, p)
		if err != nil {
			return nil, nil, "", err
		}
		closePreparationLeases(leases)
		if err := addOwner(reuseOwner{Operation: p.OperationID, Target: p.Candidate, ProbeStopped: true}); err != nil {
			return nil, nil, "", err
		}
		if p.Reuse != nil {
			for _, owner := range p.Reuse.Owners {
				if err := addOwner(owner); err != nil {
					return nil, nil, "", err
				}
			}
			for _, item := range p.Reuse.Items {
				if err := add(item); err != nil {
					return nil, nil, "", err
				}
			}
		}
		for _, claim := range p.Claims {
			if claim.Kind == "probe" || claim.Kind == "program-original" {
				continue
			}
			kind := claim.Kind
			fact := claim.Identity
			if kind == "stage" && operation != nil && operation.Phase == environment.RolledBack {
				j, err := r.Store.Load(p.Plan.OpID)
				if err != nil {
					return nil, nil, "", err
				}
				for _, root := range j.Roots {
					if root.ID == claim.Root {
						if got, e := managedRootIdentity(fact.ID, root.Failed); e == nil && got == fact {
							kind = "failed-data"
						}
					}
				}
			}
			if err := add(reuseItem{Owner: p.OperationID, Kind: kind, Root: claim.Root, Identity: fact, Intent: claim.Removed, Removed: claim.Removed}); err != nil {
				return nil, nil, "", err
			}
		}
	}
	for _, owner := range owners {
		u.Owners = append(u.Owners, owner)
	}
	for _, item := range items {
		u.Items = append(u.Items, item)
	}
	sort.Slice(u.Owners, func(i, j int) bool { return u.Owners[i].Operation < u.Owners[j].Operation })
	sort.Slice(u.Items, func(i, j int) bool { return u.Items[i].key() < u.Items[j].key() })
	if len(u.Owners) == 0 {
		if target.State != environment.Empty {
			return nil, nil, "", reuseError()
		}
		return nil, nil, "", nil
	}
	p := &EnvironmentPreparation{Params: revive.EnvironmentPlanParams{Agent: registry.Agent}, Original: current, Reuse: u}
	views, hash, err := r.inspectReuseItems(ctx, p)
	return u, views, hash, err
}

func (r *Runner) inspectReuseItems(ctx context.Context, p *EnvironmentPreparation) ([]environment.OldCleanupItem, string, error) {
	type resource struct {
		Item    reuseItem
		Tree    string
		Present bool
	}
	var resources []resource
	groups := map[string]environment.OldCleanupItem{}
	for index, item := range p.Reuse.Items {
		path, err := reuseItemPath(r.Store.Home, environment.PreparationParams{Agent: p.Params.Agent}, item)
		if err != nil {
			return nil, "", err
		}
		if err := r.protectReuse(ctx, p, item, path); err != nil {
			return nil, "", err
		}
		present, err := inspectReuse(path, item)
		if err != nil {
			return nil, "", err
		}
		kind := "program"
		if reuseDataKind(item.Kind) {
			kind = "data"
		} else if item.Kind == "workspace" {
			kind = "preparation"
		}
		key := kind + ":" + item.Root
		v := groups[key]
		v.Kind, v.Root = kind, item.Root
		entry := resource{Item: item, Present: present}
		if present {
			tree, files, bytes, err := oldCleanupTree(ctx, path, item.Kind != "workspace")
			if err != nil {
				return nil, "", err
			}
			entry.Tree = tree
			item.Tree = tree
			p.Reuse.Items[index].Tree = tree
			entry.Item = item
			v.Present = true
			if files > 1<<63-1-v.Files || bytes > 1<<63-1-v.Bytes {
				return nil, "", reuseError()
			}
			v.Files += files
			v.Bytes += bytes
		}
		groups[key] = v
		resources = append(resources, entry)
	}
	var keys []string
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var views []environment.OldCleanupItem
	for _, key := range keys {
		views = append(views, groups[key])
	}
	return views, environments.Digest(resources), nil
}
