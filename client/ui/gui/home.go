package gui

import (
	"fmt"

	"github.com/egoist/mygo/ui"
)

// fold is a persistent open/closed state for a disclosure, by key.
func (a *App) fold(key string) *bool {
	if a.folds == nil {
		a.folds = map[string]*bool{}
	}
	if a.folds[key] == nil {
		a.folds[key] = new(bool)
	}
	return a.folds[key]
}

// pageHead is a page's title and its one-line flourish.
func pageHead(c *ui.Context, title, sub string) {
	ui.Column(c).Gap(6).Children(func() {
		ui.Text(c, title).FontSize(28).Bold()
		if sub != "" {
			muted(ui.Text(c, sub)).FontSize(13)
		}
	})
}

// homePage lists the agents of every server (docs/UI.md 9.2).
func (a *App) homePage(c *ui.Context) {
	ui.Row(c).AlignItems(ui.End).Children(func() {
		ui.Column(c).Grow(1).Children(func() { pageHead(c, text.homeTitle, text.homeSub) })
		if !a.agents.busy {
			ui.Button(c, text.refresh).OnClick(func() { a.refreshAgents(); a.refreshTimeline() })
		}
	})
	res := a.agents.value
	for _, obs := range res.Observations {
		if obs.Status == "fresh" {
			continue
		}
		msg := fmt.Sprintf(text.serverUnread, obs.ConnectionID, "没有回应")
		if obs.Error != nil {
			msg = fmt.Sprintf(text.serverUnread, obs.ConnectionID, obs.Error.Reason)
		}
		if obs.Status == "stale" && obs.ObservedAt != nil {
			msg = fmt.Sprintf(text.serverStale, obs.ConnectionID, when(*obs.ObservedAt, c.Now()))
		}
		ui.Row(c.Key("obs-"+obs.ConnectionID)).Padding(12, 16).Radius(10).Background(ui.Hex("#fbf4e8")).Children(func() {
			ui.Text(c, msg).TextColor(palette.Warn)
		})
	}
	if a.agents.err != nil {
		errorBox(c, a.agents.err, a.fold("home-err"))
	}
	rows := a.agentRows(res)
	switch {
	case a.agents.busy && len(rows) == 0:
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c)
			muted(ui.Text(c, text.homeLoading)).FontSize(13)
		})
		return
	case len(rows) == 0 && a.agents.err == nil:
		muted(ui.Text(c, text.homeEmpty)).FontSize(14)
		return
	}
	ui.Text(c, text.listTitle).Bold().FontSize(14)
	card(ui.Column(c)).Padding(6, 0).Gap(0).Children(func() {
		for i, r := range rows {
			if i > 0 {
				ui.Divider(c)
			}
			a.agentLine(c.Key(r.Server+"/"+r.Instance), r)
		}
	})
	muted(ui.Text(c, text.localNote))
}

func (a *App) agentLine(c *ui.Context, r agentRow) {
	points := pointsFor(a.timeline.value, r.Server, r.Instance)
	row := ui.Row(c).Padding(14, 18).Gap(16).AlignItems(ui.Center).Cursor(ui.CursorPointer).Label(r.Name)
	if row.Hovered() {
		row.Background(ui.Hex("#faf8f4"))
	}
	row.OnClick(func() { a.openAgent(r.Server, r.Instance) }).Children(func() {
		avatar(c, r.Initial, 44)
		ui.Column(c).Grow(1).Gap(4).Children(func() {
			ui.Text(c, r.Name).FontSize(16).Bold()
			muted(ui.Text(c, r.Harness+" · "+r.Server))
			ui.Row(c).Gap(10).Children(func() {
				statusDot(c, r.Service, r.ServiceTone)
				if r.Entry != "" {
					muted(ui.Text(c, r.Entry))
				}
				if r.Note != "" {
					ui.Text(c, r.Note).FontSize(12).TextColor(palette.Warn)
				}
			})
		})
		ui.Column(c).Gap(4).AlignItems(ui.End).Children(func() {
			if len(points) > 0 {
				ui.Text(c, when(points[0].CreatedAt, c.Now())+" 已备份").FontSize(13)
				muted(ui.Text(c, "本机副本"))
			} else {
				muted(ui.Text(c, text.noLocal))
			}
		})
	})
}

func (a *App) openAgent(server, instance string) {
	a.agentTab, a.point = 0, ""
	a.router.Push("/agents/" + server + "/" + instance)
}

// avatar is the agent's initial in a soft square.
func avatar(c *ui.Context, initial string, side float32) {
	ui.Column(c).Size(side, side).Radius(side / 4).Center().Background(palette.GoldSoft).Children(func() {
		ui.Text(c, initial).FontSize(side * 0.42).Bold().TextColor(ui.Hex("#70685d"))
	})
}

// statusDot is a small colored dot with its words; the words carry the
// meaning, the color only helps.
func statusDot(c *ui.Context, label, tone string) {
	color := palette.Stop
	switch tone {
	case "ok":
		color = palette.Ok
	case "warn":
		color = palette.Warn
	}
	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Size(7, 7).Radius(4).Background(color)
		ui.Text(c, label).FontSize(12).TextColor(color)
	})
}
