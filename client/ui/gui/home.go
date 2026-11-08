package gui

import (
	"fmt"
	"sort"
	"strings"
	"time"

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

// foldDefault is fold with an initial state other than closed.
func (a *App) foldDefault(key string, open bool) *bool {
	if a.folds == nil {
		a.folds = map[string]*bool{}
	}
	if a.folds[key] == nil {
		a.folds[key] = &open
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

// homePage answers “is there anything for me to do?” first (docs/UI.md
// 7.2): a verdict, the things that need attention with their next step,
// then the agents in use as quiet rows, and the retired ones folded away.
func (a *App) homePage(c *ui.Context) {
	rows := a.agentRows(a.agents.value)
	now := c.Now()
	items := a.attentionItems(rows, now)
	var inUse, retired []agentRow
	for _, r := range rows {
		if r.Retired() {
			retired = append(retired, r)
		} else {
			inUse = append(inUse, r)
		}
	}
	ui.Row(c).AlignItems(ui.Center).Children(func() {
		muted(ui.Text(c, text.homeTitle)).FontSize(13).Grow(1)
		if a.agents.busy {
			ui.Spinner(c)
		} else {
			ui.Button(c, text.refresh).OnClick(func() { a.refreshAgents(); a.refreshTimeline() })
		}
	})
	if a.agents.busy && len(rows) == 0 {
		ui.Text(c, text.homeLoading).FontSize(26).Font(displayFont).TextColor(palette.TextSub)
		return
	}
	ui.Text(c, verdict(len(inUse), len(items))).FontSize(36).Bold().Font(displayFont).Wrap()
	if a.agents.err != nil {
		errorBox(c, a.agents.err, a.fold("home-err"))
	}
	if len(items) > 0 {
		ui.Column(c).Gap(10).Children(func() {
			for i, it := range items {
				a.attentionCard(c.Key(it.Key), it, i == 0)
			}
		})
	}
	if len(inUse) == 0 {
		if a.agents.err == nil {
			muted(ui.Text(c, text.homeEmpty)).FontSize(14)
		}
		return
	}
	// Agents with something wrong first; otherwise by name.
	sortTrouble(inUse)
	ui.Column(c).Gap(8).Margin(12, 0, 0, 0).Children(func() {
		muted(ui.Text(c, fmt.Sprintf("在用的 Agent · %d", len(inUse)))).FontSize(12).LetterSpacing(0.6)
		card(ui.Column(c)).Padding(4, 0).Gap(0).Children(func() {
			for i, r := range inUse {
				if i > 0 {
					ui.Divider(c)
				}
				a.agentLine(c.Key(r.Server+"/"+r.Instance), r)
			}
		})
	})
	if len(retired) > 0 {
		ui.Collapsible(c, fmt.Sprintf("不再使用 · %d（已迁出或已被替换）", len(retired)), a.fold("retired"), func() {
			card(ui.Column(c)).Padding(4, 0).Gap(0).Children(func() {
				for i, r := range retired {
					if i > 0 {
						ui.Divider(c)
					}
					a.agentLine(c.Key("r"+r.Server+"/"+r.Instance), r)
				}
			})
		})
	}
	muted(ui.Text(c, text.localNote)).FontSize(11)
}

// attentionCard is one thing to handle; the first card's step is the page's
// one primary button.
func (a *App) attentionCard(c *ui.Context, it attention, first bool) {
	ui.Row(c).Padding(16, 18).Gap(14).Radius(14).Background(ui.Hex("#fbf3e6")).Border(1, ui.RGBA(214, 178, 128, 0.45)).
		AlignItems(ui.Center).Children(func() {
		ui.Box(c).Size(10, 10).Radius(5).Background(palette.Warn).Shadow(0, 0, 8, 1, ui.RGBA(160, 106, 44, 0.35))
		ui.Column(c).Grow(1).Gap(4).Children(func() {
			ui.Text(c, it.Title).FontSize(15).Bold().Wrap()
			ui.Text(c, it.Reason).FontSize(13).TextColor(ui.Hex("#6b5a44")).Wrap()
		})
		if it.Next == "" {
			return
		}
		var b ui.Element
		if first {
			b = ui.PrimaryButton(c, it.Next)
		} else {
			b = ui.Button(c, it.Next)
		}
		b.OnClick(func() {
			switch {
			case it.Reread:
				a.refreshAgents()
				a.refreshTimeline()
			case it.Instance != "":
				a.openAgent(it.Server, it.Instance)
			}
		})
	})
}

func sortTrouble(rows []agentRow) {
	sort.SliceStable(rows, func(i, j int) bool { return trouble(rows[i]) && !trouble(rows[j]) })
}

func trouble(r agentRow) bool { return r.HasService && r.ServiceTone != "ok" }

// agentLine is a quiet row: who it is, and one muted line of state; only a
// problem is colored.
func (a *App) agentLine(c *ui.Context, r agentRow) {
	points := pointsFor(a.timeline.value, r.Server, r.Instance)
	var newest *time.Time
	if len(points) > 0 {
		newest = &points[0].CreatedAt
	}
	row := ui.Row(c).Padding(11, 18).Gap(14).AlignItems(ui.Center).Cursor(ui.CursorPointer).Label(r.Name)
	if row.Hovered() {
		row.Background(ui.Hex("#fbfaf6"))
	}
	row.OnClick(func() { a.openAgent(r.Server, r.Instance) }).Children(func() {
		avatar(c, a.display[r.Profile], 40, false)
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.End).Children(func() {
				ui.Text(c, r.Name).FontSize(15).Bold().MaxLines(1)
				muted(ui.Text(c, r.Harness+" · "+r.Server)).MaxLines(1)
			})
			switch {
			case r.Retired():
				muted(ui.Text(c, r.Note))
			case trouble(r):
				ui.Text(c, r.Service).FontSize(12).TextColor(palette.Warn)
			default:
				muted(ui.Text(c, quietLine(r, newest, c.Now())))
			}
		})
		ui.Text(c, "›").FontSize(18).TextColor(palette.Line)
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
