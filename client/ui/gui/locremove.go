package gui

import (
	"context"
	"slices"
	"sort"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/protection"
	"valise/core/valise"
	"valise/ui/service"
)

func (a *App) openRemoveLocation(l location) {
	ls := a.locs()
	ls.remove, ls.removeJ, ls.removeScan, ls.removeScanJ = l.Name, job{}, nil, job{}
	if l.Kind == locRepo {
		ls.removeScan = &service.TargetRemovalScan{Target: l.Name}
		return
	}
	a.scanRemove(l)
}

func (a *App) closeRemoveDialog() {
	ls := a.locs()
	ls.remove, ls.removeScan, ls.removeJ, ls.removeScanJ = "", nil, job{}, job{}
}

func (a *App) scanRemove(l location) {
	ls := a.locs()
	var scan *service.TargetRemovalScan
	a.start(&ls.removeScanJ, 2*time.Minute, func(ctx context.Context) (err error) {
		a.says(&ls.removeScanJ)(T("loc.remove_scanning"))
		scan, err = a.svc.ScanTargetRemoval(ctx, l.Name)
		return err
	}, func(err error) {
		if err == nil {
			ls.removeScan = scan
		}
	})
}

// removeDialog removes a location from Valise; what it holds stays.
func (a *App) removeDialog(c *ui.Context) {
	ls := a.locs()
	l, ok := a.locationOf(ls.remove)
	if !ok {
		a.closeRemoveDialog()
		return
	}
	if l.Kind != locRepo && ls.removeScan == nil && !ls.removeScanJ.busy && ls.removeScanJ.err == nil && !ls.removeJ.busy && ls.removeJ.err == nil {
		a.scanRemove(l)
	}
	open := true
	if ui.Modal(c, &open, func() {
		dialog(ui.Column(c), 540).Children(func() {
			dialogTitle(ui.Text(c, T("loc.remove_title", "name", l.Name)))
			ui.Text(c, T("loc.remove_what."+l.Kind)).Wrap()
			if l.Kind == locStation || l.Kind == locCloud {
				muted(ui.Text(c, T("loc.remove_yours."+l.Kind))).Wrap()
			}
			if ls.removeScanJ.busy {
				a.jobLine(c, &ls.removeScanJ)
				ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
					ui.Button(c, T("common.cancel")).OnClick(func() {
						a.stopJob(&ls.removeScanJ)
						a.closeRemoveDialog()
					})
				})
				return
			}
			if ls.removeJ.busy {
				a.jobLine(c, &ls.removeJ)
				return
			}
			if ls.removeScanJ.err != nil {
				errorBox(c, ls.removeScanJ.err, a.fold("remove-scan-err"))
				ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
					ui.Button(c, T("common.cancel")).OnClick(func() { a.closeRemoveDialog() })
					ui.PrimaryButton(c, T("loc.remove_rescan")).OnClick(func() { a.scanRemove(l) })
				})
				return
			}
			a.removeBody(c, l)
		})
	}).Dismissed() && !ls.removeJ.busy && !ls.removeScanJ.busy {
		a.closeRemoveDialog()
	}
}

func (a *App) removeBody(c *ui.Context, l location) {
	ls := a.locs()
	scan := ls.removeScan
	if scan != nil && len(scan.Unread) > 0 {
		muted(ui.Text(c, T("loc.remove_unread", "hosts", joinList(unreadServerNames(scan.Unread))))).TextColor(palette.Warn).Wrap()
	}
	blocked := scan != nil && len(scan.Blockers) > 0
	if blocked {
		a.removeBlockerPanel(c, l.Name, scan.Blockers)
	}
	if ls.removeJ.err != nil {
		errorBox(c, ls.removeJ.err, a.fold("remove-err"))
	}
	ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
		ui.Button(c, T("common.cancel")).OnClick(func() { a.closeRemoveDialog() })
		if blocked {
			ui.PrimaryButton(c, T("loc.remove_unblock_go")).OnClick(func() { a.unblockAndRemove(l) })
			return
		}
		if ue, ok := valise.As(ls.removeJ.err); ok && ue.Code == "target_in_use" {
			ui.Button(c, T("loc.remove_rescan")).OnClick(func() {
				ls.removeJ.err = nil
				a.scanRemove(l)
			})
		}
		ui.PrimaryButton(c, T("loc.remove_go")).OnClick(func() { a.removeLocation(l) })
	})
}

func (a *App) removeBlockerPanel(c *ui.Context, name string, blockers []service.TargetBlocker) {
	ui.Column(c).Padding(12, 14).Gap(6).Radius(cur.Sizes.PanelRadius).
		Background(palette.TroubleBg).Border(1, palette.TroubleBorder).Children(func() {
		ui.Text(c, T("loc.remove_blocked", "n", len(blockers), "name", name)).Bold().TextColor(palette.Warn).Wrap()
		for i, b := range blockers {
			ui.Text(c.Key("blk-"+itoa(i)), T("loc.remove_blocker_item", "host", b.Host, "agent", blockerAgentText(b))).Wrap()
		}
		muted(ui.Text(c, T("loc.remove_manual_hint"))).Wrap()
	})
}

func blockerAgentText(b service.TargetBlocker) string {
	if b.Label != "" && b.Label != b.InstanceID {
		return T("loc.remove_agent_named", "label", b.Label, "id", b.InstanceID)
	}
	return b.InstanceID
}

func unreadServerNames(unread []service.TargetScanUnread) []string {
	seen := map[string]bool{}
	var out []string
	for _, u := range unread {
		if u.Host != "" && !seen[u.Host] {
			seen[u.Host] = true
			out = append(out, u.Host)
		}
	}
	sort.Strings(out)
	return out
}

func (a *App) removeServers(l location) []string {
	seen := map[string]bool{}
	var out []string
	add := func(h string) {
		if h != "" && !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	for _, h := range a.locHosts(l) {
		if a.target(h, l.Name) != nil {
			add(h)
		}
	}
	if scan := a.locs().removeScan; scan != nil {
		for _, h := range scan.OnServers {
			add(h)
		}
	}
	sort.Strings(out)
	return out
}

func (a *App) runRemoval(ctx context.Context, l location, onServers []string, say func(string)) error {
	switch l.Kind {
	case locRepo:
		say(T("loc.remove_local"))
		return a.svc.RepoRemove(l.Name)
	case locStation, locSSH:
		for _, h := range onServers {
			say(T("loc.remove_removing", "host", h))
			if err := a.svc.TargetRemove(ctx, h, l.Name); err != nil {
				return err
			}
		}
		if l.Kind == locStation {
			say(T("loc.remove_local"))
			return a.svc.StationRemove(l.Name)
		}
	case locCloud:
		for _, h := range onServers {
			say(T("loc.remove_removing", "host", h))
			if err := a.svc.CloudRemove(ctx, h, l.Name); err != nil {
				return err
			}
		}
		if a.svc.IsCloudHere(l.Name) {
			say(T("loc.remove_local"))
			return a.svc.CloudRemoveHere(ctx, l.Name)
		}
	}
	return nil
}

func (a *App) finishRemoval() {
	a.closeRemoveDialog()
	a.locs().read = false
	if a.router.Path() != "/locations" {
		a.router.Push("/locations")
	}
}

// removeLocation forgets the location: here, and on every server that has
// a record of it (for a cloud, the servers' authorizations too), so that a
// server does not list it again. The servers go first: one that refuses,
// because a plan in force copies there, stops the removal. Nothing the
// location holds is deleted.
func (a *App) removeLocation(l location) {
	ls := a.locs()
	onServers := a.removeServers(l)
	a.start(&ls.removeJ, 5*time.Minute, func(ctx context.Context) error {
		return a.runRemoval(ctx, l, onServers, a.says(&ls.removeJ))
	}, func(err error) {
		if err == nil {
			a.finishRemoval()
		}
	})
}

// unblockAndRemove removes the location from every listed agent's protection
// plan in turn, then removes the location from all servers and locally. Any
// failure stops immediately without deleting local records.
func (a *App) unblockAndRemove(l location) {
	ls := a.locs()
	if ls.removeScan == nil {
		return
	}
	blockers := slices.Clone(ls.removeScan.Blockers)
	onServers := a.removeServers(l)
	var remaining []service.TargetBlocker
	a.start(&ls.removeJ, 10*time.Minute, func(ctx context.Context) error {
		say := a.says(&ls.removeJ)
		var err error
		remaining, err = a.svc.UnblockTarget(ctx, l.Name, blockers, service.UnblockOptions{
			ConfirmPlan: func(_ string, fc protection.FrozenChange) { a.confirm(fc.PlanID) },
			Step: func(b service.TargetBlocker, _, _ int) {
				say(T("loc.remove_unblocking", "host", b.Host, "agent", blockerAgentText(b)))
			},
		})
		if err != nil {
			return err
		}
		return a.runRemoval(ctx, l, onServers, say)
	}, func(err error) {
		updated := len(blockers) - len(remaining)
		for i := 0; i < updated; i++ {
			delete(a.prots, blockers[i].Host+"/"+blockers[i].InstanceID)
		}
		if err == nil {
			a.finishRemoval()
			return
		}
		if ls.removeScan != nil {
			ls.removeScan.Blockers = remaining
		}
	})
}
