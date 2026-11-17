package gui

import (
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
// agentLabel names an agent for assistive technology: two agents may share
// a name on different servers.
func agentLabel(r agentRow) string {
	return r.Name + T("common.comma") + r.Harness + T("common.comma") + r.Server
}

func (a *App) agentToolbar(c *ui.Context, n *Node, count, found int) {
	c.OnShortcut(ui.Cmd, ui.KeyF, func() { a.focusSearch = true })
	n.box(ui.Row(c).Gap(12).AlignItems(ui.Center).Margin(12, 0, 0, 0)).Children(func() {
		title := T("home.in_use", "n", count)
		if q := strings.TrimSpace(a.query); q != "" {
			title = T("home.found", "found", found, "n", count)
		}
		muted(ui.Text(c, title)).FontSize(12).LetterSpacing(0.6).Grow(1)
		f := ui.SearchField(c, &a.query).Width(260).Placeholder(T("home.search")).Label(T("home.search_label"))
		if a.focusSearch {
			f.Focus()
			a.focusSearch = false
		}
		ui.Row(c).Gap(2).Padding(2).Radius(9).Background(palette.Board).Border(1, palette.CardBorder).Children(func() {
			a.viewButton(c, "cards", icons.cards, T("home.view_cards"))
			a.viewButton(c, "list", icons.list, T("home.view_list"))
		})
	})
}

func (a *App) viewButton(c *ui.Context, view string, icon *ui.SVG, label string) {
	on := a.prefs.View == view || view == "cards" && a.prefs.View != "list"
	b := ui.Box(c).Size(30, 26).Radius(7).Center().Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(label).Selected(on)
	ink := palette.TextSub
	if on {
		b.Background(palette.Card).Shadow(0, 1, 2, 0, palette.Shadow.Alpha(0.12))
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
	if points := a.localPointsOf(r.Server, r.Instance); len(points) > 0 {
		return &points[0].CreatedAt
	}
	return nil
}

// cardLook is an agent card's variant (docs/UI.md 7.4.1).
type cardLook struct {
	avatar, name, pad, minH float32
	columns                 int
}

var cardLooks = map[string]cardLook{
	"normal":  {avatar: 52, name: 17, pad: 18, columns: 3},
	"large":   {avatar: 112, name: 30, pad: 32, minH: 300, columns: 1},
	"compact": {avatar: 36, name: 14, pad: 12, columns: 4},
}

// agentGrid lays the agents out as cards in the slot's variant and
// columns; one or two agents fill their row without looking lost.
func (a *App) agentGrid(c *ui.Context, n *Node, rows []agentRow) {
	look, ok := cardLooks[n.Style]
	if !ok {
		look = cardLooks["normal"]
	}
	if n.Columns > 0 {
		look.columns = n.Columns
	}
	grid := Node{Gap: n.Gap, Padding: n.Padding, Width: n.Width, MaxWidth: n.MaxWidth, Grow: n.Grow, Background: n.Background, Image: n.Image, Radius: n.Radius}
	if grid.Gap == nil {
		grid.Gap = gap(14)
	}
	grid.box(ui.Grid(c).Columns(look.columns)).Children(func() {
		for _, r := range rows {
			a.agentCard(c.Key(r.Server+"/"+r.Instance), r, look)
		}
	})
}

// agentCard is who the agent is and one line of state; a card with a
// problem is marked on its own (AGENTS.md 7.5: only problems are colored).
func (a *App) agentCard(c *ui.Context, r agentRow, look cardLook) {
	bad := trouble(r)
	box := pressableCard(card(ui.Column(c)).Padding(look.pad).Gap(14), agentLabel(r))
	if look.minH > 0 {
		box.MinHeight(look.minH).Justify(ui.SpaceBetween)
	}
	if bad { // the edge marks it; the reminder above already says why
		box.Border(1.5, palette.TroubleBorder)
	}
	box.OnClick(func() { a.openAgent(r.Server, r.Instance) }).Children(func() {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			a.avatar(c, r, look.avatar)
			ui.Column(c).Grow(1).Gap(3).Children(func() {
				ui.Text(c, r.Name).FontSize(look.name).Bold().MaxLines(1)
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
	card(ui.Column(c)).Padding(spHair + 2).Gap(0).Children(func() {
		for i, r := range rows {
			if i > 0 {
				ui.Divider(c)
			}
			a.agentLine(c.Key(key+r.Server+"/"+r.Instance), r)
		}
	})
}

func (a *App) agentLine(c *ui.Context, r agentRow) {
	row := pressable(ui.Row(c).Padding(10, 14).Radius(radPanel).Gap(14).AlignItems(ui.Center), agentLabel(r), radPanel)
	row.OnClick(func() { a.openAgent(r.Server, r.Instance) }).Children(func() {
		a.avatar(c, r, 40)
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.End).Children(func() {
				ui.Text(c, r.Name).FontSize(15).Bold().MaxLines(1)
				muted(ui.Text(c, r.Harness+" · "+r.Server)).MaxLines(1)
			})
			a.stateLine(c, r)
		})
		chevron(c)
	})
}

// textLink is a quiet, clickable line of accent text.
func textLink(c *ui.Context, label string, fn func()) {
	// The underline on hover changes what is drawn of the text itself, which
	// a Draw callback cannot do: this one hover is read as the view builds.
	t := ui.Text(c, label).FontSize(fsSub).TextColor(palette.Brand).Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(label)
	if t.Hovered() {
		t.Underline()
	}
	t.OnClick(fn)
}
