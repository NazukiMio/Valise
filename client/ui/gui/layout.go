package gui

import (
	"fmt"

	"github.com/egoist/mygo/ui"
)

// Node is one box of a theme's layout tree (docs/UI.md 7.4): a row, a
// column or a grid of children, or one of the page's slots. It holds no
// conditions, loops or expressions: it only says where slots go and how
// big they are.
type Node struct {
	Row    []Node `json:"row,omitempty"`
	Column []Node `json:"column,omitempty"`
	Grid   []Node `json:"grid,omitempty"`
	Slot   string `json:"slot,omitempty"`

	Columns    int       `json:"columns,omitempty"` // a grid's, or the agents slot's
	Gap        *float32  `json:"gap,omitempty"`
	Padding    []float32 `json:"padding,omitempty"` // 1, 2 or 4 numbers, as CSS
	Width      float32   `json:"width,omitempty"`
	Height     float32   `json:"height,omitempty"`
	MaxWidth   float32   `json:"max_width,omitempty"`
	Grow       float32   `json:"grow,omitempty"`
	Align      string    `json:"align,omitempty"`      // start, center, end, stretch
	Justify    string    `json:"justify,omitempty"`    // start, center, end, between
	Background string    `json:"background,omitempty"` // a palette color's name
	Image      string    `json:"image,omitempty"`      // one of the theme's images, covering the box
	Radius     float32   `json:"radius,omitempty"`
	Style      string    `json:"style,omitempty"` // a slot's variant
}

// maxDepth bounds a layout tree.
const maxDepth = 12

var aligns = map[string]ui.Align{"start": ui.Start, "center": ui.Center, "end": ui.End, "stretch": ui.Stretch, "between": ui.SpaceBetween}

// slotSpec is what a page offers: its slots, the variants each takes and
// the ones a theme may not leave out (docs/UI.md 7.4.1).
type slotSpec struct {
	page   string
	styles map[string][]string // slot → allowed styles; nil: no style
	always []string
	after  map[string]string // a slot that must show → its classic neighbor
}

var shellSlots = slotSpec{page: "外壳",
	styles: map[string][]string{"brand": {"icon"}, "nav": {"sidebar", "rail", "top"}, "tagline": nil, "theme": {"icon"}, "page": nil},
	always: []string{"page", "theme"}, after: map[string]string{"theme": "nav"}}

var homeSlots = slotSpec{page: "首页",
	styles: map[string][]string{"header": nil, "verdict": nil, "reminders": nil, "known": nil, "toolbar": nil,
		"agents": {"normal", "large", "compact"}, "retired": nil, "note": nil},
	always: []string{"verdict", "reminders"}, after: map[string]string{"reminders": "verdict"}}

// check finds what is wrong with a tree: unknown slots or styles, a slot
// twice, a box with two kinds, too deep, unknown colors or images.
func (s slotSpec) check(n *Node, images map[string]bool) error {
	seen := map[string]bool{}
	var walk func(n *Node, depth int) error
	walk = func(n *Node, depth int) error {
		if depth > maxDepth {
			return fmt.Errorf("%s的布局超过 %d 层", s.page, maxDepth)
		}
		kinds := 0
		for _, k := range []bool{n.Row != nil, n.Column != nil, n.Grid != nil, n.Slot != ""} {
			if k {
				kinds++
			}
		}
		if kinds != 1 {
			return fmt.Errorf("%s的布局里有一项不是恰好一种：row、column、grid 或 slot", s.page)
		}
		if n.Slot != "" {
			styles, ok := s.styles[n.Slot]
			if !ok {
				return fmt.Errorf("%s没有叫 %q 的插槽", s.page, n.Slot)
			}
			if seen[n.Slot] {
				return fmt.Errorf("%s的插槽 %q 出现了两次", s.page, n.Slot)
			}
			seen[n.Slot] = true
			if n.Style != "" && !contains(styles, n.Style) {
				return fmt.Errorf("%s的插槽 %q 没有样式 %q", s.page, n.Slot, n.Style)
			}
		} else if n.Style != "" {
			return fmt.Errorf("%s的布局里只有插槽能写 style", s.page)
		}
		if n.Align != "" && aligns[n.Align] == 0 && n.Align != "start" {
			return fmt.Errorf("%s的布局里 align 不认识 %q", s.page, n.Align)
		}
		if n.Justify != "" && aligns[n.Justify] == 0 && n.Justify != "start" {
			return fmt.Errorf("%s的布局里 justify 不认识 %q", s.page, n.Justify)
		}
		if n.Background != "" && colorFields(&Palette{})[n.Background] == nil {
			return fmt.Errorf("%s的布局里没有叫 %q 的颜色", s.page, n.Background)
		}
		if n.Image != "" && !images[n.Image] {
			return fmt.Errorf("%s的布局引用了主题里没有的图片 %q", s.page, n.Image)
		}
		if l := len(n.Padding); l != 0 && l != 1 && l != 2 && l != 4 {
			return fmt.Errorf("%s的布局里 padding 要写 1、2 或 4 个数", s.page)
		}
		for _, list := range [][]Node{n.Row, n.Column, n.Grid} {
			for i := range list {
				if err := walk(&list[i], depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(n, 1)
}

// complete puts back the slots that must show where the classic theme has
// them: after their neighbor there (the reminders after the verdict, the
// theme menu under the navigation), else at the end of the outer box; and
// says which.
func (s slotSpec) complete(n *Node) []string {
	var notes []string
	for _, slot := range s.always {
		if n.find(slot) {
			continue
		}
		add := Node{Slot: slot}
		if slot == "page" {
			add.Grow = 1
		}
		if !n.insertAfter(s.after[slot], add) {
			if n.Slot != "" { // a lone slot becomes a column
				*n = Node{Column: []Node{*n}, Grow: n.Grow}
			}
			switch {
			case n.Row != nil:
				n.Row = append(n.Row, add)
			case n.Grid != nil:
				n.Grid = append(n.Grid, add)
			default:
				n.Column = append(n.Column, add)
			}
		}
		notes = append(notes, fmt.Sprintf("%s缺少“%s”，已按经典主题补上", s.page, slotNames[slot]))
	}
	return notes
}

func (n *Node) find(slot string) bool {
	if n.Slot == slot {
		return true
	}
	for _, list := range [][]Node{n.Row, n.Column, n.Grid} {
		for i := range list {
			if list[i].find(slot) {
				return true
			}
		}
	}
	return false
}

// insertAfter puts add after the slot named anchor: beside it in a column,
// or stacked under it when the anchor sits in a row or a grid.
func (n *Node) insertAfter(anchor string, add Node) bool {
	if anchor == "" {
		return false
	}
	for _, list := range []*[]Node{&n.Row, &n.Column, &n.Grid} {
		for i := range *list {
			kid := &(*list)[i]
			if kid.Slot != anchor {
				if kid.insertAfter(anchor, add) {
					return true
				}
				continue
			}
			if list == &n.Column {
				*list = append((*list)[:i+1], append([]Node{add}, (*list)[i+1:]...)...)
			} else {
				g := kid.Grow
				kid.Grow = 0
				*kid = Node{Column: []Node{*kid, add}, Grow: g}
			}
			return true
		}
	}
	return false
}

var slotNames = map[string]string{"page": "页面区", "theme": "主题菜单", "verdict": "结论句", "reminders": "待处理提醒"}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// render draws the tree; each slot is drawn by its function with its node
// (for the slot's style and sizes).
func render(c *ui.Context, n *Node, slots map[string]func(*ui.Context, *Node)) {
	if n.Slot != "" {
		if f := slots[n.Slot]; f != nil {
			f(c, n)
		}
		return
	}
	var e ui.Element
	var kids []Node
	switch {
	case n.Row != nil:
		e, kids = ui.Row(c), n.Row
	case n.Grid != nil:
		e, kids = ui.Grid(c).Columns(max(n.Columns, 1)), n.Grid
	default:
		e, kids = ui.Column(c), n.Column
	}
	n.box(e).Children(func() {
		for i := range kids {
			render(c.Key(i), &kids[i], slots)
		}
	})
}

// box applies a node's sizes and look to an element.
func (n *Node) box(e ui.Element) ui.Element {
	if n.Gap != nil {
		e.Gap(*n.Gap)
	}
	switch p := n.Padding; len(p) {
	case 1:
		e.Padding(p[0])
	case 2:
		e.Padding(p[0], p[1])
	case 4:
		e.Padding(p[0], p[1], p[2], p[3])
	}
	if n.Width > 0 {
		e.Width(n.Width)
	}
	if n.Height > 0 {
		e.Height(n.Height)
	}
	if n.MaxWidth > 0 {
		e.MaxWidth(n.MaxWidth)
	}
	if n.Grow > 0 {
		e.Grow(n.Grow)
	}
	if a, ok := aligns[n.Align]; ok {
		e.AlignItems(a)
	}
	if a, ok := aligns[n.Justify]; ok {
		e.Justify(a)
	}
	if n.Radius > 0 {
		e.Radius(n.Radius).Clip()
	}
	if n.Background != "" {
		p := palette
		if col := colorFields(&p)[n.Background]; col != nil {
			e.Background(*col)
		}
	}
	if img := cur.Images[n.Image]; img != nil {
		e.Draw(func(p *ui.Painter, r ui.Rect) { p.Image(img, r, ui.Cover) })
	}
	return e
}

func gap(v float32) *float32 { return &v }

// classicShell is the window of the classic themes: the sidebar on the
// left, the page beside it.
func classicShell() *Node {
	return &Node{Row: []Node{
		{Column: []Node{{Slot: "brand"}, {Slot: "nav", Style: "sidebar", Grow: 1}, {Slot: "theme"}, {Slot: "tagline"}},
			Width: 212, Padding: []float32{22, 12}, Gap: gap(18), Background: "side"},
		{Slot: "page", Grow: 1, MaxWidth: 980, Padding: []float32{36, 44}},
	}, Align: "stretch"}
}

// classicHome is the home page of docs/UI.md 7.2 and 7.3.
func classicHome() *Node {
	return &Node{Column: []Node{{Slot: "header"}, {Slot: "verdict"}, {Slot: "reminders"}, {Slot: "known"},
		{Slot: "toolbar"}, {Slot: "agents", Columns: 3}, {Slot: "retired"}, {Slot: "note"}}, Gap: gap(20)}
}
