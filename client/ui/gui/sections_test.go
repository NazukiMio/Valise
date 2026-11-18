package gui

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/journey"
)

func sectionOrder(tt *ui.Tester) string {
	var got []string
	for _, text := range tt.Texts() {
		if strings.HasPrefix(text, "sec-") {
			got = append(got, strings.TrimPrefix(text, "sec-"))
		}
	}
	return strings.Join(got, ",")
}

// A page's sections flow in their own order without a tree; a tree only
// places them, and one it leaves out still shows (docs/UI.md 7.4.3).
func TestRenderSectionsFlowsByDefaultAndNeverHides(t *testing.T) {
	var tree *Node
	sections := []pageSection{}
	for _, name := range []string{"header", "status", "tabs", "content"} {
		sections = append(sections, pageSection{name, func(c *ui.Context) { ui.Text(c, "sec-"+name) }})
	}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Children(func() { renderSections(c, tree, sections) })
	}, 600, 400)
	tt.Frame()
	if got := sectionOrder(tt); got != "header,status,tabs,content" {
		t.Fatalf("classic flow: %s", got)
	}
	// Two columns side by side; "status" is left out and "tabs" named twice.
	tree = &Node{Row: []Node{
		{Column: []Node{{Slot: "content"}, {Slot: "tabs"}}, Grow: 1},
		{Column: []Node{{Slot: "header"}, {Slot: "tabs"}}, Width: 200},
	}}
	tt.Frame()
	if got := sectionOrder(tt); got != "content,tabs,header,status" {
		t.Fatalf("tree placement: %s", got)
	}
	content, _ := tt.Find("sec-content")
	header, _ := tt.Find("sec-header")
	if header.X <= content.X {
		t.Fatalf("the tree did not place header beside content: %+v %+v", header, content)
	}
}

// The agent page is drawn in sections: classic themes keep its column, and
// a theme's tree moves the blocks without touching what is in them.
func TestAgentPageSections(t *testing.T) {
	a, tt := world(t)
	a.navigate("/agents/home/xiaoman")
	tt.Frame()
	tt.Advance(time.Second)
	classic := tt.Texts()
	for _, want := range []string{T("agent.back"), "小满", T("agent.tab.overview"), T("agent.recent")} {
		if !slices.ContainsFunc(classic, func(s string) bool { return strings.Contains(s, want) }) {
			t.Fatalf("classic page lacks %q: %q", want, classic)
		}
	}
	name, _ := tt.Find("小满")
	tabs, _ := tt.Find(T("agent.tab.journey"))
	if tabs.Y <= name.Y {
		t.Fatal("classic flow: tabs are under the header")
	}
	defer delete(cur.Pages, pageAgent)
	if cur.Pages == nil {
		cur.Pages = map[string]*Node{}
		defer func() { cur.Pages = nil }()
	}
	cur.Pages[pageAgent] = &Node{Row: []Node{
		{Column: []Node{{Slot: "header"}, {Slot: "status"}, {Slot: "tabs"}}, Width: 360, Gap: gap(pageGap)},
		{Slot: "content", Grow: 1},
	}, Gap: gap(24)}
	tt.Frame()
	tt.Advance(time.Second)
	if got := tt.Texts(); len(got) != len(classic) {
		t.Fatalf("a tree changed what the page says: %d texts, classic %d", len(got), len(classic))
	}
	name, _ = tt.Find("小满")
	recent, _ := tt.Find(T("agent.recent"))
	if recent.X <= name.X {
		t.Fatalf("content is not beside the header: %+v %+v", recent, name)
	}
	if err := tt.Click(T("agent.tab.health")); err != nil || a.agentTab != tabHealth {
		t.Fatalf("tabs do not work inside a tree: %v", err)
	}
}

func TestSidebarClickReturnsToSectionRoot(t *testing.T) {
	a, tt := world(t)
	a.navigate("/agents/home/xiaoman")
	a.agentTab, a.point = tabJourney, "v1/sa"
	tt.Frame()
	tt.Advance(time.Second)
	click(t, tt, T("nav.agents"))
	if a.router.Path() != "/agents" || a.point != "" {
		t.Fatalf("sidebar click did not return to root: path=%q point=%q", a.router.Path(), a.point)
	}
}

func TestCurveScrollsHorizontallyWithManyPoints(t *testing.T) {
	a, tt := world(t)
	at := time.Now()
	var items []journey.TimelinePoint
	for i := range 10 {
		items = append(items, journey.TimelinePoint{
			SnapshotRef: journey.SnapshotRef{VaultID: "v1", SnapshotID: "p" + string(rune('a'+i))},
			CreatedAt:   at.Add(-time.Duration(i) * 26 * time.Hour), RecordedInstanceID: "xiaoman",
			Scope: journey.Scope{Tier: "full"}, FileCount: 10 + i, PlainBytes: 2048,
			Copies: []journey.Copy{{LocationRef: journey.LocationRef{Kind: "local_repo", ID: "usb"}, SourceLocator: "home/xiaoman/2026-11-01_2026-11-30"}},
		})
	}
	a.timeline.value.Items = items
	a.navigate("/agents/home/xiaoman")
	a.agentTab = tabJourney
	tt.Frame()
	tt.Advance(time.Second)
	if a.curveScroll.MaxX <= 0 {
		t.Fatalf("expected horizontal overflow for 10 points, got MaxX=%v", a.curveScroll.MaxX)
	}
	if a.curveScroll.X < a.curveScroll.MaxX-1 {
		t.Fatalf("expected curve to open at newest end (MaxX=%v), got X=%v", a.curveScroll.MaxX, a.curveScroll.X)
	}
	a.curveScroll.X = 0
	tt.Frame()
	if a.curveScroll.X != 0 {
		t.Fatalf("expected curve to hold user scroll X=0, got %v", a.curveScroll.X)
	}
}

func TestCurveAndMonthListVirtualization(t *testing.T) {
	a, tt := world(t)
	now := time.Date(2026, 11, 20, 18, 0, 0, 0, time.Local)
	var items []journey.TimelinePoint
	for i := range 30 {
		// Put newest 2 points in Nov and older 28 points in Oct so the Oct
		// collapsible stays closed and texts uniquely reflect curve cards.
		at := now.Add(-time.Duration(i) * time.Hour)
		if i >= 2 {
			at = now.AddDate(0, -1, 0).Add(-time.Duration(i) * time.Hour)
		}
		items = append(items, journey.TimelinePoint{
			SnapshotRef: journey.SnapshotRef{VaultID: "v1", SnapshotID: fmt.Sprintf("p%02d", i)},
			CreatedAt:   at, RecordedInstanceID: "xiaoman",
			Scope: journey.Scope{Tier: "full"}, FileCount: 10 + i, PlainBytes: 2048,
			Copies: []journey.Copy{{LocationRef: journey.LocationRef{Kind: "local_repo", ID: fmt.Sprintf("disk-%02d", i)}, SourceLocator: "home/xiaoman/2026-11-01_2026-11-30"}},
		})
	}
	a.timeline.value.Items = items
	a.navigate("/agents/home/xiaoman")
	a.agentTab = tabJourney
	tt.Frame()
	tt.Advance(time.Second)

	if a.curveScroll.MaxX <= 0 {
		t.Fatalf("expected horizontal overflow for 30 points, got MaxX=%v", a.curveScroll.MaxX)
	}
	if a.curveScroll.X < a.curveScroll.MaxX-1 {
		t.Fatalf("expected curve to open at newest end (MaxX=%v), got X=%v", a.curveScroll.MaxX, a.curveScroll.X)
	}
	newestLoc := journeyLocation(journey.LocationRef{Kind: "local_repo", ID: "disk-00"})
	oldestLoc := journeyLocation(journey.LocationRef{Kind: "local_repo", ID: "disk-29"})
	if !tt.HasText(newestLoc) {
		t.Fatalf("expected newest curve card %q to be instantiated", newestLoc)
	}
	if tt.HasText(oldestLoc) {
		t.Fatalf("expected off-screen oldest curve card %q to be virtualized out", oldestLoc)
	}

	// Fold the Nov month so we also confirm newest curve cards virtualize out when scrolled left.
	novMonth := T("time.year_month", "y", now.Year(), "m", int(now.Month()))
	*a.foldDefault("month-homexiaoman"+novMonth, true) = false
	a.curveScroll.X = 0
	tt.Frame()
	tt.Advance(time.Second)
	if a.curveScroll.X != 0 {
		t.Fatalf("expected curve to hold user scroll X=0, got %v", a.curveScroll.X)
	}
	if !tt.HasText(oldestLoc) {
		t.Fatalf("expected oldest curve card %q after scrolling left", oldestLoc)
	}
	if tt.HasText(newestLoc) {
		t.Fatalf("expected far-right curve card %q to be virtualized out when scrolled left", newestLoc)
	}

	// Now put all 30 points in the same expanded month (> monthListThreshold) and
	// verify ui.List virtualizes the month rows instead of instantiating all 30.
	for i := range items {
		items[i].CreatedAt = now.Add(-time.Duration(i) * 30 * time.Minute)
	}
	*a.foldDefault("month-homexiaoman"+novMonth, true) = true
	a.timeline.value.Items = items
	tt.Frame()
	tt.Advance(time.Second)
	firstRowFiles := T("point.files", "n", 10, "size", size(2048))
	lastRowFiles := T("point.files", "n", 39, "size", size(2048))
	if !tt.HasText(firstRowFiles) {
		t.Fatalf("expected top month row %q in virtual list", firstRowFiles)
	}
	if tt.HasText(lastRowFiles) {
		t.Fatalf("expected bottom month row %q to be virtualized out by ui.List", lastRowFiles)
	}
}
