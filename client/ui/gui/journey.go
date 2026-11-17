package gui

import (
	"github.com/egoist/mygo/ui"

	"valise/core/journey"
)

// journey shows the saved points of one agent: the newest on the gold line,
// then all of them along a thin line, newest first, grouped by month; a
// point opens its details. Only successful backups are points (docs/UI.md
// 10.2). An agent that migrated also has its route (docs/UI.md 7.1).
func (a *App) journey(c *ui.Context, row *agentRow, points []journey.TimelinePoint) {
	if a.point != "" {
		a.journeyReadNotes(c)
		a.journeyFailedLocations(c)
		for i, p := range points {
			if pointKey(p) == a.point {
				a.pointDetail(c, points, i, row.Server, row.Instance)
				return
			}
		}
		a.closePoint()
	}
	pageHead(c, T("journey.title", "name", row.Name), T("journey.sub"))
	a.journeyReadNotes(c)
	stops := route(a.agents.value.Items, row.Server, row.Instance)
	if len(stops) > 0 {
		ui.Row(c).Children(func() { ui.Segmented(c, &a.journeyMode, T("journey.view.points"), T("journey.view.route")) })
		if a.journeyMode == 1 {
			routeView(c, stops)
			a.journeyFailedLocations(c)
			return
		}
	}
	if a.timeline.err != nil {
		errorBox(c, a.timeline.err, a.fold("timeline-err"))
	}
	if len(points) == 0 {
		a.journeyFailedLocations(c)
		if a.timeline.busy {
			ui.Spinner(c)
		} else {
			muted(ui.Text(c, T("journey.empty"))).FontSize(14)
		}
		return
	}
	a.curveStrip(c, points)
	a.journeyFailedLocations(c)
	// The full list stays below, folded by month; only the newest month is
	// open (docs/UI.md 7.2).
	muted(ui.Text(c, T("journey.count", "n", len(points)))).FontSize(12).LetterSpacing(0.6).Margin(8, 0, 0, 0)
	var months []string
	byMonth := map[string][]journey.TimelinePoint{}
	for _, p := range points {
		m := T("time.year_month", "y", p.CreatedAt.Local().Year(), "m", int(p.CreatedAt.Local().Month()))
		if byMonth[m] == nil {
			months = append(months, m)
		}
		byMonth[m] = append(byMonth[m], p)
	}
	for mi, m := range months {
		open := a.foldDefault("month-"+row.Server+row.Instance+m, mi == 0)
		ui.Collapsible(c.Key("m"+m), T("journey.month", "month", m, "n", len(byMonth[m])), open, func() {
			// One column without gaps, so the line runs unbroken.
			ui.Column(c).Gap(0).Children(func() {
				list := byMonth[m]
				for i, p := range list {
					a.pointLine(c.Key(pointKey(p)), p, i == 0, i == len(list)-1)
				}
			})
		})
	}
	muted(ui.Text(c, T("journey.all_locations")))
	// Legacy catalog key "journey.local_only" remains for locale compatibility.
}

// pointLine is a node on the thin line and a one-line card.
func (a *App) pointLine(c *ui.Context, p journey.TimelinePoint, first, last bool) {
	ui.Row(c).Gap(12).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Width(16).AlignItems(ui.Center).Children(func() {
			top := ui.Box(c).Width(1).Height(13).Background(palette.Line)
			if first {
				top.Background(ui.RGBA(0, 0, 0, 0))
			}
			ui.Box(c).Size(8, 8).Radius(4).Background(palette.Node).Border(1.5, palette.Gold)
			bottom := ui.Box(c).Width(1).Grow(1).Background(palette.Line)
			if last {
				bottom.Background(ui.RGBA(0, 0, 0, 0))
			}
		})
		box := pressable(ui.Row(c).Grow(1).Padding(spInside, spItem).Gap(spItem).Radius(radPanel).Margin(0, 0, spHair, 0).AlignItems(ui.Center),
			when(p.CreatedAt, c.Now()), radPanel)
		box.OnClick(func() { a.point = pointKey(p) }).Children(func() {
			ui.Text(c, when(p.CreatedAt, c.Now())).FontSize(13).Bold().Width(cur.Sizes.TimeWidth).Wrap()
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				muted(ui.Text(c, tierText(p.Scope.Tier)))
				muted(ui.Text(c, pointLocations(p))).FontSize(fsCaption).Wrap()
			})
			muted(ui.Text(c, T("point.files", "n", p.FileCount, "size", size(p.PlainBytes))))
		})
	})
}

// pointDetail shows one saved point; earlier and later ones are a step
// away. Recovery sources are resolved only from configured readers.
func (a *App) pointDetail(c *ui.Context, points []journey.TimelinePoint, i int, origin, instance string) {
	p := points[i]
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		if ui.Button(c, "‹ "+T("point.back")).Clicked() {
			a.closePoint()
		}
		ui.Spacer(c)
		if i+1 < len(points) && ui.Button(c, T("point.earlier")).Clicked() {
			a.point = pointKey(points[i+1])
		}
		if i > 0 && ui.Button(c, T("point.later")).Clicked() {
			a.point = pointKey(points[i-1])
		}
	})
	pageHead(c, when(p.CreatedAt, c.Now()), p.CreatedAt.Local().Format("2006-01-02 15:04:05"))
	card(ui.Column(c)).Children(func() {
		ui.Text(c, T("point.carried")).Bold()
		fact(c, T("point.scope"), tierText(p.Scope.Tier))
		fact(c, T("point.content"), T("point.files", "n", p.FileCount, "size", size(p.PlainBytes)))
	})
	card(ui.Column(c)).Children(func() {
		ui.Text(c, T("point.copies")).Bold()
		for _, cp := range p.Copies {
			ui.Column(c.Key(cp.LocationRef.ID + cp.SourceLocator)).Gap(2).Children(func() {
				ui.Text(c, journeyLocation(cp.LocationRef)).FontSize(13)
				muted(ui.Text(c, T("point.records_only")))
			})
		}
	})
	if p.MetadataState == "conflict" {
		ui.Text(c, T("point.conflict")).TextColor(palette.Warn).Wrap()
	}
	if len(p.Warnings) > 0 {
		card(ui.Column(c)).Children(func() {
			ui.Text(c, T("point.warn")).Bold().TextColor(palette.Warn)
			for _, w := range p.Warnings {
				if w == "snapshot_time_uses_source_clock" {
					w = T("point.source_clock")
				}
				ui.Text(c, w).FontSize(13)
			}
		})
	}
	if p.MetadataState != "conflict" && len(a.svc.RestoreSources(p)) > 0 {
		ui.PrimaryButton(c, T("restore.use_point")).OnClick(func() { a.openRestore(p, origin) })
		if len(a.testSources(p)) > 0 {
			ui.Button(c, T("restoretest.point_open")).OnClick(func() { a.openPointTest(p, origin, instance) })
		}
	} else if p.MetadataState != "conflict" {
		muted(ui.Text(c, T("restore.source_unavailable"))).Wrap()
	}
	ui.Collapsible(c, T("error.tech_info"), a.fold("point-tech"), func() {
		muted(ui.Text(c, T("point.ids", "snapshot", p.SnapshotRef.SnapshotID, "vault", p.SnapshotRef.VaultID))).Font(cur.Fonts.Mono)
	})
}

// Legacy catalog key "point.repo" remains for locale compatibility.
