package gui

import (
	"fmt"
	"sort"
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
// 7.2, 7.3): a verdict, the reminders still open with their two buttons,
// then the agents as cards (or a list) under a search, and the retired ones
// folded away.
func (a *App) homePage(c *ui.Context) {
	rows := a.agentRows(a.agents.value)
	now := c.Now()
	var open, known []attention
	for _, it := range a.attentionItems(rows, now) {
		if st, ok := a.prefs.Dismissed[it.Key]; ok && st == it.State {
			known = append(known, it)
		} else {
			open = append(open, it)
		}
	}
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
	ui.Column(c).Gap(6).Children(func() {
		ui.Text(c, verdict(len(inUse), len(open))).FontSize(36).Bold().Font(displayFont).Wrap()
		if len(known) > 0 {
			shown := a.fold("known")
			label := fmt.Sprintf("另有 %d 件已知道 ›", len(known))
			if *shown {
				label = fmt.Sprintf("收起已知道的 %d 件", len(known))
			}
			textLink(c, label, func() { *shown = !*shown })
		}
	})
	if a.agents.err != nil {
		errorBox(c, a.agents.err, a.fold("home-err"))
	}
	if len(open) > 0 {
		ui.Column(c).Gap(10).Children(func() {
			for i, it := range open {
				a.attentionCard(c.Key(it.Key), it, i == 0)
			}
		})
	}
	if len(known) > 0 && *a.fold("known") {
		ui.Column(c).Gap(8).Children(func() {
			for _, it := range known {
				a.knownCard(c.Key("k"+it.Key), it)
			}
		})
	}
	if len(inUse) == 0 {
		if a.agents.err == nil {
			muted(ui.Text(c, text.homeEmpty)).FontSize(14)
		}
		return
	}
	a.agentToolbar(c, len(inUse))
	shown := filterAgents(inUse, a.query)
	sortTrouble(shown)
	switch {
	case len(shown) == 0:
		muted(ui.Text(c, "没有找到“"+a.query+"”。可以按名字、harness 或服务器搜索。")).FontSize(14)
	case a.prefs.View == "list":
		a.agentList(c, "l", shown)
	default:
		a.agentGrid(c, "g", shown)
	}
	if retired = filterAgents(retired, a.query); len(retired) > 0 {
		ui.Collapsible(c, fmt.Sprintf("不再使用 · %d（已迁出或已被替换）", len(retired)), a.fold("retired"), func() {
			a.agentList(c, "r", retired)
		})
	}
	muted(ui.Text(c, text.localNote)).FontSize(11)
}

func sortTrouble(rows []agentRow) {
	sort.SliceStable(rows, func(i, j int) bool { return trouble(rows[i]) && !trouble(rows[j]) })
}

func trouble(r agentRow) bool { return r.HasService && r.ServiceTone != "ok" }

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
