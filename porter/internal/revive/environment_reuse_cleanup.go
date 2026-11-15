package revive

import (
	"context"
	"strconv"
)

func (r *Runner) preparationReuseCleanupItem(ctx context.Context, p *EnvironmentPreparation, index int) (PreparationCleanupItem, error) {
	root := strconv.Itoa(index)
	item, path, err := r.reuseCleanupItem(p, root)
	if err != nil {
		return PreparationCleanupItem{}, err
	}
	if err := r.protectReuse(ctx, p, item, path); err != nil {
		return PreparationCleanupItem{}, err
	}
	present, err := inspectReuse(path, item)
	if err != nil {
		return PreparationCleanupItem{}, err
	}
	view := PreparationCleanupItem{Kind: "reuse", Root: root, Present: present, EmptyOnly: item.EmptyOnly, Identity: item.Identity, DisplayKind: "reuse-program"}
	view.Identity.ID = "reuse:" + root
	if reuseDataKind(item.Kind) {
		view.DisplayKind = "reuse-data"
	} else if item.Kind == "workspace" {
		view.DisplayKind = "reuse-workspace"
	}
	if present {
		view.Tree, _, _, err = oldCleanupTree(ctx, path, item.Kind != "workspace")
	}
	return view, err
}

func (r *Runner) beginReuseCleanupItem(ctx context.Context, p *EnvironmentPreparation, root, expectedTree string) error {
	item, path, err := r.reuseCleanupItem(p, root)
	if err != nil {
		return err
	}
	if err := r.protectReuse(ctx, p, item, path); err != nil {
		return err
	}
	if present, err := inspectReuse(path, item); err != nil || !present {
		return reuseError()
	}
	if tree, _, _, err := oldCleanupTree(ctx, path, item.Kind != "workspace"); err != nil || tree != expectedTree {
		return reuseError()
	}
	index, _ := strconv.Atoi(root)
	p.Reuse.Items[index].Intent = true
	return savePreparation(r.Store.Home, p, false)
}

func (r *Runner) completeReuseCleanupItem(p *EnvironmentPreparation, root string) error {
	_, _, err := r.reuseCleanupItem(p, root)
	if err != nil {
		return err
	}
	index, _ := strconv.Atoi(root)
	p.Reuse.Items[index].Intent, p.Reuse.Items[index].Removed = true, true
	return nil
}
