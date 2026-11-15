package gui

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"valise/core/journey"
)

func journeyLocation(loc journey.LocationRef) string {
	switch loc.Kind {
	case "server_vault":
		return T("point.location.server", "name", loc.ID)
	case "local_repo":
		return T("point.location.local", "name", loc.ID)
	case "cloud":
		return T("point.location.cloud", "name", loc.ID)
	case "legacy_target":
		return T("point.location.remote", "name", loc.ID, "server", loc.AuthorityID)
	}
	return T("point.location.other", "name", loc.ID)
}

func pointLocations(p journey.TimelinePoint) string {
	var names []string
	seen := map[journey.LocationRef]bool{}
	for _, cp := range p.Copies {
		if !seen[cp.LocationRef] {
			name := journeyLocation(cp.LocationRef)
			if cp.Observation.Status != "fresh" {
				name += T("point.location.unverified")
			}
			names = append(names, name)
			seen[cp.LocationRef] = true
		}
	}
	return strings.Join(names, T("common.dot_sep"))
}

func (a *App) journeyReadNotes(c *ui.Context) {
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		if a.timeline.busy {
			ui.Spinner(c)
			muted(ui.Text(c, T("journey.scanning", "n", len(a.timelinePending)))).Grow(1).Wrap()
		} else {
			muted(ui.Text(c, T("journey.scan_done"))).Grow(1)
			ui.Button(c, T("journey.refresh")).OnClick(a.refreshTimeline)
		}
	})
	failed := 0
	for _, obs := range a.timeline.value.Observations {
		if obs.Status != "fresh" && obs.Error != "refreshing" {
			failed++
		}
	}
	if failed == 0 {
		return
	}
	ui.Collapsible(c, T("journey.failed_locations", "n", failed), a.fold("journey-errors"), func() {
		for _, obs := range a.timeline.value.Observations {
			if obs.Status == "fresh" || obs.Error == "refreshing" {
				continue
			}
			ui.Row(c.Key(obs.LocationRef.Kind + obs.LocationRef.AuthorityID + obs.LocationRef.ID)).Gap(8).AlignItems(ui.Center).Children(func() {
				key := "journey.unavailable"
				switch obs.Error {
				case "unsupported_capability":
					key = "journey.old_porter"
				case "location_timeout":
					key = "journey.timeout"
				}
				ui.Column(c).Grow(1).Gap(2).Children(func() {
					muted(ui.Text(c, T(key, "location", journeyLocation(obs.LocationRef)))).Wrap()
					if obs.Status == "stale" {
						muted(ui.Text(c, T("journey.kept_records", "at", when(obs.ObservedAt, c.Now())))).Wrap()
					}
				})
				if !a.timeline.busy {
					ui.Button(c, T("journey.retry")).OnClick(func() { a.readTimeline([]journey.LocationRef{obs.LocationRef}) })
				}
			})
		}
	})
}

func pointKey(p journey.TimelinePoint) string {
	return p.SnapshotRef.VaultID + "/" + p.SnapshotRef.SnapshotID
}

// Legacy locale keys remain compatible: "journey.partial", "journey.stale".
