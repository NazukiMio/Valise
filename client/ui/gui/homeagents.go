package gui

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

// The agents on the home page (docs/UI.md 7.3): cards by default, a list
// when chosen, the choice kept on this computer; a search above them
// filters by name, harness and server, and ⌘F goes to it.

// filterAgents keeps the rows whose name, harness, server or instance
// contains the query, ignoring case.
func filterAgents(rows []agentRow, query string) []agentRow {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return rows
	}
	var out []agentRow
	for _, r := range rows {
		for _, s := range []string{r.Name, r.Harness, r.Server, r.Instance} {
			if strings.Contains(strings.ToLower(s), q) {
				out = append(out, r)
				break
			}
		}
	}
	return out
}

// agentToolbar is the section's title, the search and the view switch.
func (a *App) agentToolbar(c *ui.Context, count int) {
	c.OnShortcut(ui.Cmd, ui.KeyF, func() { a.focusSearch = true })
	ui.Row(c).Gap(12).AlignItems(ui.Center).Margin(12, 0, 0, 0).Children(func() {
		muted(ui.Text(c, fmt.Sprintf("在用的 Agent · %d", count))).FontSize(12).LetterSpacing(0.6).Grow(1)
		f := ui.SearchField(c, &a.query).Width(260).Placeholder("搜索名字、harness 或服务器  ⌘F").Label("搜索 Agent")
		if a.focusSearch {
			f.Focus()
			a.focusSearch = false
		}
		ui.Row(c).Gap(2).Padding(2).Radius(9).Background(palette.Board).Border(1, palette.CardBorder).Children(func() {
			a.viewButton(c, "cards", icons.cards, "卡片视图")
			a.viewButton(c, "list", icons.list, "列表视图")
		})
	})
}

func (a *App) viewButton(c *ui.Context, view string, icon *ui.SVG, label string) {
	on := a.prefs.View == view || view == "cards" && a.prefs.View != "list"
	b := ui.Box(c).Size(30, 26).Radius(7).Center().Cursor(ui.CursorPointer).Label(label).Selected(on)
	ink := palette.TextSub
	if on {
		b.Background(palette.Card).Shadow(0, 1, 2, 0, ui.RGBA(60, 40, 20, 0.12))
		ink = palette.Text
	}
	b.OnClick(func() {
		if a.prefs.View != view {
			a.prefs.View = view
			a.savePrefs()
		}
	}).Children(func() {
		ui.Icon(c, icon).FontSize(16).TextColor(ink)
	})
}

// newest is the agent's latest local backup time, nil for none.
func (a *App) newest(r agentRow) *time.Time {
	if points := pointsFor(a.timeline.value, r.Server, r.Instance); len(points) > 0 {
		return &points[0].CreatedAt
	}
	return nil
}

// agentGrid lays the agents out as cards, three to a row; one or two
// agents fill their row without looking lost.
func (a *App) agentGrid(c *ui.Context, key string, rows []agentRow) {
	ui.Grid(c).Columns(3).Gap(14).Children(func() {
		for _, r := range rows {
			a.agentCard(c.Key(key+r.Server+"/"+r.Instance), r)
		}
	})
}

// agentCard is who the agent is and one line of state; a card with a
// problem is marked on its own (AGENTS.md 7.5: only problems are colored).
func (a *App) agentCard(c *ui.Context, r agentRow) {
	bad := trouble(r)
	box := card(ui.Column(c)).Padding(18).Gap(14).Cursor(ui.CursorPointer).Label(r.Name)
	if bad {
		box.Border(1, ui.RGBA(214, 150, 90, 0.7)).Background(ui.Hex("#fffaf2"))
	} else if box.Hovered() {
		box.Border(1, palette.Line).Shadow(0, 6, 18, 0, ui.RGBA(60, 40, 20, 0.08))
	}
	box.OnClick(func() { a.openAgent(r.Server, r.Instance) }).Children(func() {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			a.avatar(c, r, 52)
			ui.Column(c).Grow(1).Gap(3).Children(func() {
				ui.Text(c, r.Name).FontSize(17).Bold().MaxLines(1)
				muted(ui.Text(c, r.Harness)).MaxLines(1)
			})
		})
		ui.Column(c).Gap(4).Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, icons.server).FontSize(13).TextColor(palette.TextSub)
				muted(ui.Text(c, r.Server)).MaxLines(1)
			})
			a.stateLine(c, r)
		})
	})
}

// stateLine is the one line of state under an agent.
func (a *App) stateLine(c *ui.Context, r agentRow) {
	switch {
	case r.Retired():
		muted(ui.Text(c, r.Note)).MaxLines(2)
	case trouble(r):
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(7, 7).Radius(4).Background(palette.Warn)
			ui.Text(c, r.Service).FontSize(12).Bold().TextColor(palette.Warn)
		})
	default:
		muted(ui.Text(c, quietLine(r, a.newest(r), c.Now()))).MaxLines(2)
	}
}

// agentList is the same agents as quiet rows.
func (a *App) agentList(c *ui.Context, key string, rows []agentRow) {
	card(ui.Column(c)).Padding(4, 0).Gap(0).Children(func() {
		for i, r := range rows {
			if i > 0 {
				ui.Divider(c)
			}
			a.agentLine(c.Key(key+r.Server+"/"+r.Instance), r)
		}
	})
}

func (a *App) agentLine(c *ui.Context, r agentRow) {
	row := ui.Row(c).Padding(11, 18).Gap(14).AlignItems(ui.Center).Cursor(ui.CursorPointer).Label(r.Name)
	if row.Hovered() {
		row.Background(ui.Hex("#fbfaf6"))
	}
	row.OnClick(func() { a.openAgent(r.Server, r.Instance) }).Children(func() {
		a.avatar(c, r, 40)
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.End).Children(func() {
				ui.Text(c, r.Name).FontSize(15).Bold().MaxLines(1)
				muted(ui.Text(c, r.Harness+" · "+r.Server)).MaxLines(1)
			})
			a.stateLine(c, r)
		})
		ui.Text(c, "›").FontSize(18).TextColor(palette.Line)
	})
}

// textLink is a quiet, clickable line of accent text.
func textLink(c *ui.Context, label string, fn func()) {
	t := ui.Text(c, label).FontSize(13).TextColor(palette.Brand).Cursor(ui.CursorPointer).Label(label)
	if t.Hovered() {
		t.Underline()
	}
	t.OnClick(fn)
}
