package gui

import (
	"context"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/journey"
	"valise/ui/service"
)

func (a *App) openJourneyDeleteModal(st *snapDeleteState, points []journey.TimelinePoint) {
	st.leafBySnap = map[string]string{}
	for _, p := range points {
		if st.selected[p.SnapshotRef.SnapshotID] {
			if lf := pointLeaf(p); lf != "" {
				st.leafBySnap[p.SnapshotRef.SnapshotID] = lf
			}
		}
	}
	st.locations = deleteLocationsFromPoints(points, st.selected)
	if len(st.locations) == 0 && st.host != "" {
		st.locations = []service.SnapshotDeleteLocation{{Kind: service.SnapshotLocationServerVault}}
	}
	st.checkedLoc = map[string]bool{}
	st.preview, st.result, st.j = nil, nil, job{}
	st.open = true
}

func (a *App) openSinglePointDelete(row *agentRow, p journey.TimelinePoint) {
	st := a.snapDeleteFor("journey:"+row.Server+"/"+row.Instance, row.Server, a.serverName(row.Server))
	st.selected = map[string]bool{p.SnapshotRef.SnapshotID: true}
	a.openJourneyDeleteModal(st, []journey.TimelinePoint{p})
}

func (a *App) openLocationSnapDelete(l location, host string, r periodRow) {
	scope := "loc:" + l.Name + "/" + host + "/" + r.leaf
	st := a.snapDeleteFor(scope, host, a.serverName(host))
	st.candidates = append([]string(nil), r.here...)
	st.leafBySnap = map[string]string{}
	st.selected = map[string]bool{}
	for _, id := range r.here {
		st.leafBySnap[id] = r.leaf
	}
	var locs []service.SnapshotDeleteLocation
	if r.server != nil {
		locs = append(locs, service.SnapshotDeleteLocation{Kind: service.SnapshotLocationServerVault})
	}
	switch l.Kind {
	case locRepo:
		locs = append(locs, service.SnapshotDeleteLocation{Kind: service.SnapshotLocationLocalRepo, Name: l.Name})
	case locStation:
		locs = append(locs, service.SnapshotDeleteLocation{Kind: service.SnapshotLocationStation, Name: l.Name})
	case locCloud:
		if a.svc.IsCloudHere(l.Name) {
			locs = append(locs, service.SnapshotDeleteLocation{Kind: service.SnapshotLocationLocalCloud, Name: l.Name})
		} else {
			locs = append(locs, service.SnapshotDeleteLocation{Kind: service.SnapshotLocationServerTarget, Name: l.Name})
		}
	default:
		locs = append(locs, service.SnapshotDeleteLocation{Kind: service.SnapshotLocationServerTarget, Name: l.Name})
	}
	st.locations = locs
	st.checkedLoc = map[string]bool{}
	st.preview, st.result, st.j = nil, nil, job{}
	st.open = true
}

func (a *App) journeyDeleteBar(c *ui.Context, row *agentRow, points []journey.TimelinePoint) *snapDeleteState {
	st := a.snapDeleteFor("journey:"+row.Server+"/"+row.Instance, row.Server, a.serverName(row.Server))
	ui.Row(c).Gap(spInside).AlignItems(ui.Center).Wrap().Children(func() {
		if !st.selecting {
			quietButton(c, T("snapdel.manage")).OnClick(func() {
				st.selecting = true
				st.selected = map[string]bool{}
			})
			return
		}
		n := len(st.selectedIDs())
		muted(ui.Text(c, T("snapdel.selected_count", "n", n))).FontSize(fsSub)
		ui.Button(c, T("snapdel.delete_selected", "n", n)).Disabled(n == 0).OnClick(func() {
			a.openJourneyDeleteModal(st, points)
		})
		quietButton(c, T("common.cancel")).OnClick(func() {
			st.selecting = false
			st.selected = map[string]bool{}
		})
	})
	return st
}

func (a *App) snapDeleteDialog(c *ui.Context) {
	st := a.snapDelete
	if st == nil || !st.open {
		return
	}
	if ui.Modal(c, &st.open, func() {
		dialog(ui.Column(c), 520).Children(func() {
			dialogTitle(ui.Text(c, T("snapdel.title")))
			if len(st.candidates) > 0 && st.preview == nil && st.result == nil {
				ui.Text(c, T("snapdel.pick_snapshots")).Bold()
				subPanel(ui.Column(c)).Gap(spHair).Children(func() {
					for _, id := range st.candidates {
						checked := st.selected[id]
						if ui.Checkbox(c.Key("sd-s-"+id), &checked, id).Changed() {
							st.selected[id] = checked
						}
					}
				})
			}
			if st.result != nil {
				a.snapDeleteResultView(c, st)
				return
			}
			if st.preview == nil {
				a.snapDeletePickLocations(c, st)
				return
			}
			a.snapDeleteConfirmView(c, st)
		})
	}).Dismissed() && !st.j.busy {
		st.open = false
	}
}

func (a *App) snapDeletePickLocations(c *ui.Context, st *snapDeleteState) {
	selectedCount := len(st.selectedIDs())
	unresolved := st.unresolvedCount()
	reqs := st.requestsByLeaf()
	muted(ui.Text(c, T("snapdel.pick_locations_hint", "n", selectedCount))).FontSize(fsSub).Wrap()
	if selectedCount > 0 && len(reqs) == 0 {
		notice(c, "warn", T("snapdel.invalid_leaf"))
	} else if unresolved > 0 {
		notice(c, "warn", T("snapdel.unresolved_leaf", "n", unresolved))
	}
	subPanel(ui.Column(c)).Gap(spInside).Children(func() {
		for _, loc := range st.locations {
			k := deleteLocKey(loc)
			checked := st.checkedLoc[k]
			if ui.Checkbox(c.Key("sd-l-"+k), &checked, deleteLocLabel(loc)).Changed() {
				st.checkedLoc[k] = checked
			}
		}
	})
	if st.j.busy {
		a.jobLine(c, &st.j)
		return
	}
	if st.j.err != nil {
		errorBox(c, st.j.err, a.fold("snapdel-preview-err"))
	}
	canPreview := len(reqs) > 0 && len(st.chosenLocations()) > 0
	ui.Row(c).Gap(spInside).Justify(ui.End).Children(func() {
		ui.Button(c, T("common.cancel")).OnClick(func() { st.open = false })
		ui.PrimaryButton(c, T("snapdel.preview_go")).Disabled(!canPreview).OnClick(func() { a.runSnapDeletePreview(st) })
	})
}

func (a *App) snapDeleteConfirmView(c *ui.Context, st *snapDeleteState) {
	muted(ui.Text(c, T("snapdel.confirm_summary", "snaps", len(st.preview.SnapshotIDs), "locs", len(st.preview.Locations)))).FontSize(fsSub).Wrap()
	if len(st.preview.BundleImpacts) > 0 {
		for _, imp := range st.preview.BundleImpacts {
			members := strings.Join(imp.BundleMembers, T("common.list_sep"))
			notice(c.Key("sd-imp-"+imp.SnapshotID), "warn", T("snapdel.bundle_impact", "id", imp.SnapshotID, "members", members))
		}
	}
	if st.j.busy {
		a.jobLine(c, &st.j)
		return
	}
	if st.j.err != nil {
		errorBox(c, st.j.err, a.fold("snapdel-run-err"))
	}
	ui.Row(c).Gap(spInside).Justify(ui.End).Children(func() {
		ui.Button(c, T("common.back")).OnClick(func() { st.preview = nil })
		dangerButton(c, T("snapdel.confirm_go")).OnClick(func() { a.runSnapDeleteExecute(st) })
	})
}

func (a *App) snapDeleteResultView(c *ui.Context, st *snapDeleteState) {
	if st.result.Partial {
		notice(c, "warn", T("snapdel.result_partial"))
	} else {
		notice(c, "ok", T("snapdel.result_ok"))
	}
	subPanel(ui.Column(c)).Gap(spInside).Children(func() {
		for _, r := range st.result.Results {
			label := deleteLocLabel(r.Location)
			if r.Error != "" {
				statusRow(c, "warn", label, T("snapdel.loc_err", "reason", r.Error))
			} else {
				statusRow(c, "ok", label, T("snapdel.loc_ok", "deleted", r.Deleted, "packs", r.PacksRemoved, "freed", size(r.FreedBytes)))
			}
		}
	})
	ui.Row(c).Justify(ui.End).Children(func() {
		ui.PrimaryButton(c, T("common.close")).OnClick(func() {
			st.open = false
			st.selecting = false
			st.selected = map[string]bool{}
		})
	})
}

func (a *App) runSnapDeletePreview(st *snapDeleteState) {
	reqs := st.requestsByLeaf()
	if len(reqs) == 0 {
		return
	}
	fn := a.previewSnapDeleteFn
	if fn == nil {
		fn = a.svc.PreviewSnapshotDelete
	}
	var merged *service.SnapshotDeletePreview
	a.start(&st.j, time.Minute, func(ctx context.Context) error {
		out := &service.SnapshotDeletePreview{Host: st.host, Locations: reqs[0].Locations}
		for _, req := range reqs {
			p, err := fn(ctx, req)
			if err != nil {
				return err
			}
			out.Leaf = p.Leaf
			out.SnapshotIDs = append(out.SnapshotIDs, p.SnapshotIDs...)
			out.BundleImpacts = append(out.BundleImpacts, p.BundleImpacts...)
		}
		merged = out
		return nil
	}, func(err error) {
		if err == nil {
			st.preview = merged
		}
	})
}

func (a *App) runSnapDeleteExecute(st *snapDeleteState) {
	reqs := st.requestsByLeaf()
	if len(reqs) == 0 {
		return
	}
	fn := a.deleteSnapshotsFn
	if fn == nil {
		fn = a.svc.DeleteSnapshots
	}
	var merged *service.SnapshotDeleteBatchResult
	a.start(&st.j, 5*time.Minute, func(ctx context.Context) error {
		out := &service.SnapshotDeleteBatchResult{Host: st.host}
		for _, req := range reqs {
			res, err := fn(ctx, req)
			if err != nil {
				return err
			}
			out.Leaf = res.Leaf
			out.Partial = out.Partial || res.Partial
			out.Results = append(out.Results, res.Results...)
		}
		merged = out
		return nil
	}, func(err error) {
		if err == nil {
			st.result = merged
			a.refreshTimeline()
			if a.loc != nil || strings.HasPrefix(st.scope, "loc:") {
				a.readLocations()
			}
		}
	})
}
