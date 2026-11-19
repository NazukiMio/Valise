package gui

import (
	"context"
	"strings"
	"time"

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

// pageAgent names the agent page's tree in a theme (Theme.Pages).
const pageAgent = "agent"

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
	points := a.pointsOf(server, instance)
	renderSections(c, cur.Pages[pageAgent], []pageSection{
		{"header", func(c *ui.Context) { a.agentHeader(c, row) }},
		{"status", func(c *ui.Context) {
			if a.iconErr != nil {
				errorBox(c, a.iconErr, a.fold("icon-err"))
			}
			a.entryReport(c, row)
		}},
		{"tabs", func(c *ui.Context) {
			if ui.Tabs(c, &a.agentTab, T("agent.tab.overview"), T("agent.tab.journey"), T("agent.tab.health"), T("agent.tab.manage")).Changed() {
				a.closePoint()
			}
		}},
		{"content", func(c *ui.Context) {
			switch a.agentTab {
			case tabJourney:
				a.journey(c, row, points)
			case tabHealth:
				a.healthTab(c, row)
			case tabManage:
				a.manageTab(c, row)
				if !row.Retired() {
					a.agentKitCard(c, row)
				}
			default:
				a.overview(c, row, points)
			}
		}},
	})
	a.agentRenameDialog(c, row)
}

type agentRenameState struct {
	server, instance, value string
	j                       job
}

func (a *App) agentRenameDialog(c *ui.Context, row *agentRow) {
	rn := a.agentRename
	if rn == nil || rn.server != row.Server || rn.instance != row.Instance {
		return
	}
	open := true
	if ui.Modal(c, &open, func() {
		dialog(ui.Column(c), 420).Children(func() {
			dialogTitle(ui.Text(c, T("agent.rename_title")))
			muted(ui.Text(c, T("agent.rename_hint"))).FontSize(fsSub).Wrap()
			labeled(c, T("agent.rename_label"), func() {
				ui.TextInput(c, &rn.value).Label(T("agent.rename_label")).Placeholder(row.Instance).AutoFocus()
			})
			if rn.j.busy {
				a.jobLine(c, &rn.j)
				return
			}
			if rn.j.err != nil {
				errorBox(c, rn.j.err, a.fold("agent-rename-"+rn.server+rn.instance))
			}
			ui.Row(c).Gap(8).AlignItems(ui.Center).Justify(ui.End).Children(func() {
				needTag(c, T("need.valise"))
				ui.Button(c, T("common.cancel")).OnClick(func() { a.agentRename = nil })
				ui.PrimaryButton(c, T("agent.rename_save")).OnClick(func() {
					val := strings.TrimSpace(rn.value)
					fn := a.setAgentLabel
					if fn == nil {
						fn = a.svc.SetAgentLabel
					}
					a.start(&rn.j, time.Minute, func(ctx context.Context) error {
						return fn(ctx, rn.server, rn.instance, val)
					}, func(err error) {
						if err == nil {
							for i := range a.agents.value.Items {
								it := &a.agents.value.Items[i]
								if it.ConnectionID == rn.server && it.Instance.InstanceID == rn.instance {
									it.Instance.Label = val
								}
							}
							a.agentRename = nil
							if a.setAgentLabel == nil {
								a.refreshAgents()
							}
						}
					})
				})
			})
		})
	}).Dismissed() && !rn.j.busy {
		a.agentRename = nil
	}
}

// agentHeader is who it is, one sentence about it, and the page's one
// primary action.
func (a *App) agentHeader(c *ui.Context, row *agentRow) {
	if backLink(c, T("agent.back"), "/agents").Clicked() {
		a.closePoint()
	}
	newest := a.newest(*row)
	ui.Row(c).Gap(18).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Gap(6).AlignItems(ui.Center).Children(func() {
			a.avatar(c, *row, 76)
			a.iconChoice(c, row)
		})
		ui.Column(c).Grow(1).Gap(6).Children(func() {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Wrap().Children(func() {
				ui.Text(c, row.Name).FontSize(32).Bold().Font(cur.Fonts.Display)
				if !row.Retired() {
					quietButton(c, T("agent.rename_open")).OnClick(func() {
						a.agentRename = &agentRenameState{server: row.Server, instance: row.Instance, value: row.Label}
					})
				}
			})
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
}

func (a *App) overview(c *ui.Context, row *agentRow, points []journey.TimelinePoint) {
	// The details: each fact in its own cell (docs/UI.md 7.2).
	statusBoard(c, agentCells(*row, points, c), false)
	ui.Row(c).Gap(18).AlignItems(ui.Start).Children(func() {
		card(ui.Column(c)).Grow(1).Basis(0).Children(func() {
			cardTitle(ui.Text(c, T("agent.recent")))
			if len(points) == 0 {
				muted(ui.Text(c, T("backup.no_points")))
			}
			for i, p := range points {
				if i == 3 {
					break
				}
				// Each recent point opens itself in the journey.
				pressable(ui.Row(c.Key(pointKey(p))).Gap(10).Padding(spHair+2, spInside).Radius(radControl).AlignItems(ui.Center), when(p.CreatedAt, c.Now()), radControl).
					OnClick(func() { a.agentTab, a.point = tabJourney, pointKey(p) }).Children(func() {
					ui.Text(c, when(p.CreatedAt, c.Now())).FontSize(fsBody)
					muted(ui.Text(c, tierText(p.Scope.Tier))).Grow(1)
					chevron(c)
				})
			}
			if len(points) > 0 {
				ui.Row(c).Children(func() {
					textLink(c, T("journey.all"), func() { a.agentTab = tabJourney })
				})
			}
		})
		card(ui.Column(c)).Grow(1).Basis(0).Children(func() {
			cardTitle(ui.Text(c, T("agent.where", "server", row.Server)))
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
	muted(ui.Text(c, T("backup.sources_note")))
	if !row.Retired() { // the advanced entry comes after what the page is about
		a.nativeCard(c, row)
	}
}

// fact is one labelled line of a card; the label's width is the theme's,
// and a label longer than it wraps (docs/UI.md 7.4.3).
func fact(c *ui.Context, label, value string) {
	ui.Row(c).Gap(14).Children(func() {
		if label != "" {
			muted(ui.Text(c, label)).Width(cur.Sizes.LabelWidth).FontSize(13).Wrap()
		}
		ui.Text(c, value).FontSize(13).Grow(1)
	})
}

// Legacy catalog key "backup.no_local" is retained for locale compatibility.
