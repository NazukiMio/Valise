package gui

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
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
