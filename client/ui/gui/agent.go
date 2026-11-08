package gui

import (
	"fmt"

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
	ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
		avatar(c, row.Initial, 56)
		ui.Column(c).Gap(6).Children(func() {
			ui.Text(c, row.Name).FontSize(26).Bold()
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				label := row.Server
				if row.Harness != "" {
					label = row.Harness + " · " + row.Server
				}
				muted(ui.Text(c, label)).FontSize(13)
				if row.Service != "" {
					statusDot(c, row.Service, row.ServiceTone)
				}
			})
		})
	})
	if ui.Tabs(c, &a.agentTab, text.tabOverview, text.tabJourney).Changed() {
		a.point = ""
	}
	points := pointsFor(a.timeline.value, server, instance)
	if a.agentTab == 1 {
		a.journey(c, row, points)
		return
	}
	a.overview(c, row, points)
}

func (a *App) overview(c *ui.Context, row *agentRow, points []journey.TimelinePoint) {
	ui.Row(c).Gap(18).AlignItems(ui.Start).Children(func() {
		card(ui.Column(c)).Grow(1).Children(func() {
			ui.Text(c, fmt.Sprintf(text.whereTitle, row.Server)).Bold()
			fact(c, text.harnessLabel, row.Harness)
			fact(c, text.serviceLabel, row.Service)
			if row.Entry != "" {
				fact(c, text.entryLabel, row.Entry)
			}
			if row.Note != "" {
				fact(c, "", row.Note)
			}
			if len(row.Roots) > 0 {
				ui.Collapsible(c, text.techInfo, a.fold("roots-"+row.Server+row.Instance), func() {
					for _, r := range row.Roots {
						muted(ui.Text(c, r)).Font("monospace")
					}
				})
			}
		})
		card(ui.Column(c)).Width(300).Children(func() {
			ui.Text(c, text.recentTitle).Bold()
			if len(points) == 0 {
				muted(ui.Text(c, text.noLocal))
			}
			for i, p := range points {
				if i == 3 {
					break
				}
				ui.Column(c.Key(p.SnapshotRef.SnapshotID)).Gap(2).Children(func() {
					ui.Text(c, when(p.CreatedAt, c.Now())).FontSize(14)
					muted(ui.Text(c, tierText(p.Scope.Tier)))
				})
			}
			if len(points) > 0 && ui.Button(c, text.journeyAll).Clicked() {
				a.agentTab = 1
			}
		})
	})
	muted(ui.Text(c, text.localNote))
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
