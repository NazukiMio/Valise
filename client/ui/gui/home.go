package gui

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"valise/core/journey"
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
		ui.Text(c, title).FontSize(30).Bold().Font(displayFont)
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
	row := ui.Row(c).Padding(16, 18).Gap(18).AlignItems(ui.Center).Cursor(ui.CursorPointer).Label(r.Name)
	if row.Hovered() {
		row.Background(ui.Hex("#fbfaf6"))
	}
	row.OnClick(func() { a.openAgent(r.Server, r.Instance) }).Children(func() {
		avatar(c, r.Initial, 44)
		ui.Column(c).Width(190).Gap(4).Children(func() {
			ui.Text(c, r.Name).FontSize(16).Bold().MaxLines(1)
			muted(ui.Text(c, r.Harness+" · "+r.Server)).MaxLines(1)
			if r.Note != "" {
				ui.Text(c, r.Note).FontSize(12).TextColor(palette.Warn)
			}
		})
		ui.Column(c).Grow(1).Children(func() { statusBoard(c, agentCells(r, points, c), true) })
	})
}

// agentCells are the facts of one agent's status board, each in its own
// cell: the service, the admin page's port, the newest backup this computer
// holds and where it is kept.
func agentCells(r agentRow, points []journey.TimelinePoint, c *ui.Context) []cell {
	cells := []cell{{Label: "服务", Value: shortService(r.Service), Tone: r.ServiceTone}}
	if r.Entry != "" {
		tone, v := "stop", "没有监听"
		if r.Entry == text.entryListening {
			tone, v = "ok", "在监听"
		}
		cells = append(cells, cell{Label: "管理页端口", Value: v, Tone: tone})
	}
	if len(points) == 0 {
		return append(cells, cell{Label: "最近备份", Value: "本机还没有", Tone: "warn"})
	}
	var repos []string
	seen := map[string]bool{}
	for _, cp := range points[0].Copies {
		if !seen[cp.LocationRef.ID] {
			seen[cp.LocationRef.ID] = true
			repos = append(repos, cp.LocationRef.ID)
		}
	}
	return append(cells, cell{Label: "最近备份", Value: when(points[0].CreatedAt, c.Now())},
		cell{Label: "本机副本", Value: strings.Join(repos, "、")})
}

// shortService drops the “服务” the board's label already says.
func shortService(s string) string {
	if t, ok := strings.CutPrefix(s, "服务"); ok {
		return t
	}
	return s
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
