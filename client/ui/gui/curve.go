package gui

import (
	"math"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/journey"
)

// The journey's gold line (docs/UI.md 7.1, 10.1): the newest saved points,
// oldest on the left, strung on a slowly drifting curve. The curve's rise and
// fall is only a guide for the eye: it means nothing about health. A click
// lights the point's glow, and about 300ms later its details open; with
// reduced motion the line is still and the details open at once.

const (
	curveHeight = 260
	curveBand   = 64 // room for the cards above and below the line
	curvePoints = 6
	glowDelay   = 300 * time.Millisecond
)

// curveY is the line's height at t in [0,1] for a box of height h.
func curveY(t, h, drift float32) float32 {
	mid, amp := h/2, h/2-curveBand-14
	return mid + amp*float32(math.Sin(2*math.Pi*0.85*float64(t)+0.6+float64(drift)))
}

// curveStrip draws up to curvePoints of points (newest first in points).
func (a *App) curveStrip(c *ui.Context, points []journey.TimelinePoint) {
	n := min(len(points), curvePoints)
	shown := make([]journey.TimelinePoint, n)
	for i := range n {
		shown[i] = points[n-1-i]
	}
	still := c.Preferences().ReduceMotion
	a.settleGlow(c, still)
	box := ui.Box(c).Height(curveHeight).Radius(16).Background(ui.Hex("#fbf9f4")).Border(1, palette.CardBorder).Clip()
	box.Draw(func(p *ui.Painter, r ui.Rect) {
		drift := float32(0)
		if !still {
			sec := float64(p.Now().UnixMilli()%24000) / 24000
			drift = 0.18 * float32(math.Sin(2*math.Pi*sec))
			p.After(50 * time.Millisecond)
		}
		// A soft wash of gold behind the line, then the line itself.
		p.FillGradient(r, ui.LinearGradient{Angle: 100,
			Stops: []ui.GradientStop{{Color: ui.RGBA(245, 230, 205, 0.0), At: 0}, {Color: ui.RGBA(245, 230, 205, 0.45), At: 0.5}, {Color: ui.RGBA(245, 230, 205, 0.0), At: 1}}}, 16)
		var line ui.Path
		for i := 0; i <= 120; i++ {
			t := float32(i) / 120
			x, y := r.X+r.W*t, r.Y+curveY(t, r.H, drift)
			if i == 0 {
				line.MoveTo(x, y)
			} else {
				line.LineTo(x, y)
			}
		}
		p.StrokePath(&line, 6, ui.RGBA(239, 230, 214, 0.7))
		p.StrokePath(&line, 1.6, palette.Gold)
		for i, pt := range shown {
			t := (float32(i) + 0.5) / float32(n)
			x, y := r.X+r.W*t, r.Y+curveY(t, r.H, drift)
			stem := r.Y + curveBand
			if y < r.Y+r.H/2 {
				stem = r.Y + r.H - curveBand
			}
			p.Line(x, y, x, stem, 1, ui.RGBA(212, 192, 163, 0.6))
			if pt.SnapshotRef.SnapshotID == a.glow {
				k := min(float32(p.Now().Sub(a.glowAt))/float32(glowDelay), 1)
				for j, rad := range []float32{30, 20, 12} {
					p.FillPath((&ui.Path{}).Circle(x, y, rad*(0.5+0.5*k)), ui.RGBA(212, 192, 163, 0.18*float32(j+1)*k))
				}
			}
			p.FillPath((&ui.Path{}).Circle(x, y, 6), ui.Hex("#ffffff"))
			p.StrokePath((&ui.Path{}).Circle(x, y, 6), 1.6, palette.Gold)
			p.FillPath((&ui.Path{}).Circle(x, y, 2), ui.Hex("#8c7e6c"))
		}
	})
	box.Children(func() {
		ui.Row(c).Fill().Children(func() {
			for i, pt := range shown {
				t := (float32(i) + 0.5) / float32(n)
				top := curveY(t, curveHeight, 0) > curveHeight/2
				col := ui.Column(c.Key(pt.SnapshotRef.SnapshotID)).Grow(1).Basis(0).FillHeight().AlignItems(ui.Center).Padding(10, 6)
				if top {
					col.Justify(ui.Start)
				} else {
					col.Justify(ui.End)
				}
				col.Children(func() { a.curveCard(c, pt) })
			}
		})
	})
}

func (a *App) curveCard(c *ui.Context, pt journey.TimelinePoint) {
	id := pt.SnapshotRef.SnapshotID
	card := ui.Column(c).Gap(2).Padding(7, 11).Radius(10).Background(palette.Card).Border(1, palette.CardBorder).
		Shadow(0, 4, 14, 0, ui.RGBA(62, 54, 44, 0.06)).Cursor(ui.CursorPointer).Label(when(pt.CreatedAt, c.Now()))
	if card.Hovered() || a.glow == id {
		card.Border(1, palette.Gold).Shadow(0, 0, 18, 0, ui.RGBA(212, 192, 163, 0.7))
	}
	card.OnClick(func() { a.lightPoint(id) }).Children(func() {
		ui.Text(c, when(pt.CreatedAt, c.Now())).FontSize(12).Bold().MaxLines(1)
		muted(ui.Text(c, tierText(pt.Scope.Tier))).FontSize(11).MaxLines(1)
	})
}

// lightPoint starts the glow; settleGlow opens the details once it has had
// its moment (or at once with reduced motion).
func (a *App) lightPoint(id string) {
	a.glow, a.glowAt = id, time.Time{}
}

func (a *App) settleGlow(c *ui.Context, still bool) {
	if a.glow == "" {
		return
	}
	if a.glowAt.IsZero() {
		a.glowAt = c.Now()
	}
	if left := glowDelay - c.Now().Sub(a.glowAt); left > 0 && !still {
		c.After(left)
		return
	}
	a.point, a.glow = a.glow, ""
	c.Invalidate() // this frame already chose the list; show the details next
}
