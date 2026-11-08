package gui

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/journey"
)

// agentPage is one agent: overview and journey (docs/UI.md 9.5, 10.1).
// Health and settings tabs come with their segments; until then they are
// not drawn.
func (a *App) agentPage(c *ui.Context, server, instance string) {
	var row *agentRow
	for _, r := range a.agentRows(a.agents.value) {
		if r.Server == server && r.Instance == instance {
			r := r
			row = &r
		}
	}
	if row == nil {
		row = &agentRow{Server: server, Instance: instance, Name: instance, Initial: initial(instance)}
	}
	if ui.Link(c, "‹ "+text.back, "/agents").Clicked() {
		a.point = ""
	}
	points := pointsFor(a.timeline.value, server, instance)
	var newest *time.Time
	if len(points) > 0 {
		newest = &points[0].CreatedAt
	}
	// Who it is, one sentence about it, and the page's one primary action.
	ui.Row(c).Gap(18).AlignItems(ui.Center).Children(func() {
		avatar(c, a.display[row.Profile], 76, true)
		ui.Column(c).Grow(1).Gap(6).Children(func() {
			ui.Text(c, row.Name).FontSize(32).Bold().Font(displayFont)
			label := row.Server
			if row.Harness != "" {
				label = row.Harness + " · " + row.Server
			}
			muted(ui.Text(c, label)).FontSize(13)
			say, bad := agentVerdict(*row, newest, c.Now())
			v := ui.Text(c, say).FontSize(15).Wrap()
			if bad {
				v.TextColor(palette.Warn)
			}
		})
		if row.Entry != "" && !row.Retired() {
			a.entryButton(c, row)
		}
	})
	a.entryReport(c, row)
	if ui.Tabs(c, &a.agentTab, text.tabOverview, text.tabJourney).Changed() {
		a.point = ""
	}
	if a.agentTab == 1 {
		a.journey(c, row, points)
		return
	}
	a.overview(c, row, points)
}

func (a *App) overview(c *ui.Context, row *agentRow, points []journey.TimelinePoint) {
	// The details: each fact in its own cell (docs/UI.md 7.2).
	statusBoard(c, agentCells(*row, points, c), false)
	ui.Row(c).Gap(18).AlignItems(ui.Start).Children(func() {
		card(ui.Column(c)).Grow(1).Basis(0).Children(func() {
			ui.Text(c, text.recentTitle).Bold()
			if len(points) == 0 {
				muted(ui.Text(c, text.noLocal))
			}
			for i, p := range points {
				if i == 3 {
					break
				}
				ui.Row(c.Key(p.SnapshotRef.SnapshotID)).Gap(10).AlignItems(ui.End).Children(func() {
					ui.Text(c, when(p.CreatedAt, c.Now())).FontSize(14)
					muted(ui.Text(c, tierText(p.Scope.Tier)))
				})
			}
			if len(points) > 0 {
				ui.Row(c).Children(func() {
					if ui.Button(c, text.journeyAll).Clicked() {
						a.agentTab = 1
					}
				})
			}
		})
		card(ui.Column(c)).Grow(1).Basis(0).Children(func() {
			ui.Text(c, fmt.Sprintf(text.whereTitle, row.Server)).Bold()
			fact(c, text.harnessLabel, row.Harness)
			if len(row.Roots) > 0 {
				ui.Collapsible(c, text.techInfo, a.fold("roots-"+row.Server+row.Instance), func() {
					for _, r := range row.Roots {
						muted(ui.Text(c, r)).Font("monospace")
					}
				})
			}
		})
	})
	muted(ui.Text(c, text.localNote)).FontSize(11)
}

// fact is one labelled line of a card.
func fact(c *ui.Context, label, value string) {
	ui.Row(c).Gap(14).Children(func() {
		if label != "" {
			muted(ui.Text(c, label)).Width(80).FontSize(13)
		}
		ui.Text(c, value).FontSize(13).Grow(1)
	})
}
