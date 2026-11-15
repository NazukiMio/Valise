package gui

import (
	"github.com/egoist/mygo/ui"

	"valise/core/journey"
)

// The agent page's tabs. Settings come with their segment; until then the
// tab is not drawn.
const (
	tabOverview = iota
	tabJourney
	tabHealth
	tabManage
)

// agentPage is one agent: overview, journey and health (docs/UI.md 9.5,
// 10.1, 7.3).
func (a *App) agentPage(c *ui.Context, server, instance string) {
	a.ensureTimelineScope(server, instance)
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
	if ui.Link(c, "‹ "+T("agent.back"), "/agents").Clicked() {
		a.point = ""
	}
	points := a.pointsOf(server, instance)
	newest := a.newest(*row)
	// Who it is, one sentence about it, and the page's one primary action.
	ui.Row(c).Gap(18).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Gap(6).AlignItems(ui.Center).Children(func() {
			a.avatar(c, *row, 76)
			a.iconChoice(c, row)
		})
		ui.Column(c).Grow(1).Gap(6).Children(func() {
			ui.Text(c, row.Name).FontSize(32).Bold().Font(cur.Fonts.Display)
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
	if a.iconErr != nil {
		errorBox(c, a.iconErr, a.fold("icon-err"))
	}
	a.entryReport(c, row)
	if ui.Tabs(c, &a.agentTab, T("agent.tab.overview"), T("agent.tab.journey"), T("agent.tab.health"), T("agent.tab.manage")).Changed() {
		a.point = ""
	}
	switch a.agentTab {
	case tabJourney:
		a.journey(c, row, points)
		return
	case tabHealth:
		a.healthTab(c, row)
		return
	case tabManage:
		a.manageTab(c, row)
		return
	}
	a.overview(c, row, points)
}

func (a *App) overview(c *ui.Context, row *agentRow, points []journey.TimelinePoint) {
	if !row.Retired() {
		a.nativeCard(c, row)
	}
	// The details: each fact in its own cell (docs/UI.md 7.2).
	statusBoard(c, agentCells(*row, points, c), false)
	ui.Row(c).Gap(18).AlignItems(ui.Start).Children(func() {
		card(ui.Column(c)).Grow(1).Basis(0).Children(func() {
			ui.Text(c, T("agent.recent")).Bold()
			if len(points) == 0 {
				muted(ui.Text(c, T("backup.no_points")))
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
					if ui.Button(c, T("journey.all")).Clicked() {
						a.agentTab = tabJourney
					}
				})
			}
		})
		card(ui.Column(c)).Grow(1).Basis(0).Children(func() {
			ui.Text(c, T("agent.where", "server", row.Server)).Bold()
			fact(c, T("agent.harness"), row.Harness)
			if len(row.Roots) > 0 {
				ui.Collapsible(c, T("error.tech_info"), a.fold("roots-"+row.Server+row.Instance), func() {
					for _, r := range row.Roots {
						muted(ui.Text(c, r)).Font(cur.Fonts.Mono)
					}
				})
			}
		})
	})
	muted(ui.Text(c, T("backup.sources_note"))).FontSize(11)
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

// Legacy catalog key "backup.no_local" is retained for locale compatibility.
