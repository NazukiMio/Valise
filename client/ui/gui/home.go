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
		ui.Text(c, title).FontSize(cur.Sizes.Title).Bold().Font(cur.Fonts.Display)
		if sub != "" {
			muted(ui.Text(c, sub)).FontSize(13)
		}
	})
}

// homeData is what the home page's slots show.
type homeData struct {
	open, known    []attention
	inUse, retired []agentRow
	loading        bool
}

// homePage answers “is there anything for me to do?” first (docs/UI.md
// 7.2, 7.3); the theme's layout says where each part goes (7.4).
func (a *App) homePage(c *ui.Context) {
	rows := a.agentRows(a.agents.value)
	d := &homeData{loading: a.agents.busy && len(rows) == 0}
	for _, it := range a.attentionItems(rows, c.Now()) {
		if st, ok := a.prefs.Dismissed[it.Key]; ok && st == it.State {
			d.known = append(d.known, it)
		} else {
			d.open = append(d.open, it)
		}
	}
	for _, r := range rows {
		if r.Retired() {
			d.retired = append(d.retired, r)
		} else {
			d.inUse = append(d.inUse, r)
		}
	}
	page := *cur.Home
	render(c, &page, map[string]func(*ui.Context, *Node){
		"header":    a.headerSlot,
		"verdict":   func(c *ui.Context, n *Node) { a.verdictSlot(c, n, d) },
		"reminders": func(c *ui.Context, n *Node) { a.remindersSlot(c, n, d) },
		"known":     func(c *ui.Context, n *Node) { a.knownSlot(c, n, d) },
		"toolbar":   func(c *ui.Context, n *Node) { a.toolbarSlot(c, n, d) },
		"agents":    func(c *ui.Context, n *Node) { a.agentsSlot(c, n, d) },
		"retired":   func(c *ui.Context, n *Node) { a.retiredSlot(c, n, d) },
		"note":      func(c *ui.Context, n *Node) { muted(n.box(ui.Text(c, text.localNote))).FontSize(11) },
	})
}

func (a *App) headerSlot(c *ui.Context, n *Node) {
	n.box(ui.Row(c).AlignItems(ui.Center)).Children(func() {
		muted(ui.Text(c, text.homeTitle)).FontSize(13).Grow(1)
		if a.agents.busy {
			ui.Spinner(c)
		} else {
			ui.Button(c, text.refresh).OnClick(func() { a.refreshAgents(); a.refreshTimeline() })
		}
	})
}

func (a *App) verdictSlot(c *ui.Context, n *Node, d *homeData) {
	n.box(ui.Column(c).Gap(6)).Children(func() {
		if d.loading {
			ui.Text(c, text.homeLoading).FontSize(26).Font(cur.Fonts.Display).TextColor(palette.TextSub)
			return
		}
		ui.Text(c, verdict(len(d.inUse), len(d.open))).FontSize(cur.Sizes.Verdict).Bold().Font(cur.Fonts.Display).Wrap()
		if len(d.known) > 0 {
			shown := a.fold("known")
			label := fmt.Sprintf("另有 %d 件已知道 ›", len(d.known))
			if *shown {
				label = fmt.Sprintf("收起已知道的 %d 件", len(d.known))
			}
			textLink(c, label, func() { *shown = !*shown })
		}
		if a.agents.err != nil {
			errorBox(c, a.agents.err, a.fold("home-err"))
		}
	})
}

func (a *App) remindersSlot(c *ui.Context, n *Node, d *homeData) {
	if len(d.open) == 0 {
		return
	}
	n.box(ui.Column(c).Gap(10)).Children(func() {
		for i, it := range d.open {
			a.attentionCard(c.Key(it.Key), it, i == 0)
		}
	})
}

func (a *App) knownSlot(c *ui.Context, n *Node, d *homeData) {
	if len(d.known) == 0 || !*a.fold("known") {
		return
	}
	n.box(ui.Column(c).Gap(8)).Children(func() {
		for _, it := range d.known {
			a.knownCard(c.Key("k"+it.Key), it)
		}
	})
}

func (a *App) toolbarSlot(c *ui.Context, n *Node, d *homeData) {
	if d.loading || len(d.inUse) == 0 {
		return
	}
	a.agentToolbar(c, n, len(d.inUse), len(filterAgents(d.inUse, a.query)))
}

func (a *App) agentsSlot(c *ui.Context, n *Node, d *homeData) {
	if d.loading {
		return
	}
	if len(d.inUse) == 0 {
		if a.agents.err == nil {
			muted(ui.Text(c, text.homeEmpty)).FontSize(14)
		}
		return
	}
	shown := filterAgents(d.inUse, a.query)
	sortTrouble(shown)
	switch {
	case len(shown) == 0:
		muted(ui.Text(c, "没有找到“"+a.query+"”。可以按名字、harness 或服务器搜索。")).FontSize(14)
	case a.prefs.View == "list":
		a.agentList(c, "l", shown)
	default:
		a.agentGrid(c, n, shown)
	}
}

func (a *App) retiredSlot(c *ui.Context, n *Node, d *homeData) {
	if retired := filterAgents(d.retired, a.query); len(retired) > 0 {
		ui.Collapsible(c, fmt.Sprintf("不再使用 · %d（已迁出或已被替换）", len(retired)), a.fold("retired"), func() {
			a.agentList(c, "r", retired)
		})
	}
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
