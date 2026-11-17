package gui

import (
	"github.com/egoist/mygo/ui"
	"slices"
)

type restoreLocation struct {
	location, host, leaf string
	snapshots            []string
}

func (a *App) canRestoreLocation(l location) bool {
	return l.Kind == locRepo || l.Kind == locCloud && a.svc.IsCloudHere(l.Name)
}
func (a *App) restoreLocationPoints(c *ui.Context) {
	selected := a.restoreLocation
	if selected == nil {
		return
	}
	a.ensureTimelineScope(selected.host, "")
	backLink(c, T("nav.locations"), "/locations/"+selected.location)
	pageHead(c, T("restore.location_points"), selected.location+" / "+selected.host+" / "+periodText(selected.leaf))
	if a.timeline.busy {
		ui.Spinner(c)
	}
	found := false
	for _, p := range a.timeline.value.Items {
		if !slices.Contains(selected.snapshots, p.SnapshotRef.SnapshotID) {
			continue
		}
		for _, src := range a.svc.RestoreSources(p) {
			if src.Copy.LocationRef.ID != selected.location {
				continue
			}
			found = true
			card(ui.Row(c.Key(pointKey(p)))).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Grow(1).Children(func() {
					ui.Text(c, p.CreatedAt.Local().Format("2006-01-02 15:04:05")).Bold()
					muted(ui.Text(c, tierText(p.Scope.Tier))).Wrap()
				})
				ui.Button(c, T("restore.use_point")).OnClick(func() {
					a.openRestore(p, selected.host)
					if a.restore != nil && !a.restore.j.busy {
						for i, source := range a.restore.sources {
							if source.Copy.LocationRef.ID == selected.location {
								a.restore.source = i
								break
							}
						}
					}
				})
			})
			break
		}
	}
	if !found {
		muted(ui.Text(c, T("restore.location_empty"))).Wrap()
	}
	ui.Button(c, T("restore.refresh_points")).OnClick(a.refreshTimeline)
}
