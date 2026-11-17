package gui

import (
	"math"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/journey"
)

// The journey's line (docs/UI.md 7.3, 10.1): a spine that breathes slowly,
// with fine hair strands streaming along it and fading at their tips, after
// the user's reference page; the newest saved points hang on the spine,
// oldest on the left. Its rise and fall is only a guide for the eye: it
// means nothing about health. A click lights the point's glow, and about
// 300ms later its details open; with reduced motion everything is still
// and the details open at once.

const (
	curveHeight     = 300
	curveBand       = 66 // room for the cards above and below the spine
	curvePoints     = 6
	curvePointWidth = 148
	glowDelay       = 300 * time.Millisecond
	hairCount       = 32
)

// spineY is the spine's height at horizontal pixel offset px in a strip of
// width w and height h at animation time t. Up to ~780 DIPs the wave spans
// the whole strip; wider scrollable strips continue at the same wavelength.
func spineY(px, w, h, t float32) float32 {
	if cur.Timeline == TimelinePlain {
		return h / 2 // the plain line is straight and still
	}
	period := max(min(w, 780), 1)
	x := px / period
	s := h / 300
	w1 := math.Sin(float64(x)*math.Pi*2.1+float64(t)*0.55) * 24
	w2 := math.Cos(float64(x)*math.Pi*1.3-float64(t)*0.32) * 11
	return h/2 + float32(w1+w2)*s
}

// hair is one strand; its numbers come from its index so every frame and
// every run draws the same strands.
type hair struct {
	length, start, offset, freq, amp, phase, alpha, width, speed float32
}

var hairs = func() []hair {
	r := func(i, k int) float32 { // a fixed number in [0,1) for (i, k)
		v := math.Sin(float64(i*12+k)*12.9898+78.233) * 43758.5453
		return float32(v - math.Floor(v))
	}
	out := make([]hair, hairCount)
	for i := range out {
		l := 0.20 + r(i, 1)*0.36
		out[i] = hair{length: l, start: float32(i)/hairCount*1.5 - 0.35 + (r(i, 2)-0.5)*0.18,
			offset: (r(i, 3) - 0.5) * 13, freq: 0.004 + r(i, 4)*0.003, amp: 1.8 + r(i, 5)*3.2,
			phase: r(i, 6) * 2 * math.Pi, alpha: 0.2 + r(i, 7)*0.3, width: 0.45 + r(i, 8)*0.45,
			speed: (0.00018 + r(i, 9)*0.00028) * 60}
	}
	return out
}()

// paintHairs draws the strands (in the strands variant) and the spine into
// r; the gold variant is the spine alone, a little stronger.
func paintHairs(p *ui.Painter, r ui.Rect, t float32) {
	span := float32(1.2 + 0.56 + 0.27)
	unit := min(r.W, 880)
	if unit <= 0 {
		return
	}
	tiles := max(int(math.Ceil(float64(r.W/unit))), 1)
	if cur.Timeline == TimelineStrands {
		for m := range tiles {
			base := float32(m) * unit
			for _, h := range hairs {
				x0 := h.start + float32(m)*0.37 + h.speed*t/0.54
				x0 = float32(math.Mod(float64(x0+0.83), float64(span))) - 0.83
				pxA := max(base+x0*unit, 0)
				pxB := min(base+(x0+h.length)*unit, r.W)
				if pxB <= pxA || h.length*unit <= 0 {
					continue
				}
				var path ui.Path
				first := true
				for x := pxA; x <= pxB; x += 6 {
					y := spineY(x, r.W, r.H, t) + h.offset + h.amp*float32(math.Sin(float64(x*h.freq+t*0.4+h.phase)))
					if first {
						path.MoveTo(r.X+x, r.Y+y)
						first = false
					} else {
						path.LineTo(r.X+x, r.Y+y)
					}
				}
				// Strands fade toward their tips: thinner and lighter where short.
				fade := min(1, (pxB-pxA)/(h.length*unit)*1.4)
				p.StrokePath(&path, h.width, palette.Strand.Alpha(h.alpha*fade))
			}
		}
	}
	var spine ui.Path
	for x := float32(0); x <= r.W; x += 4 {
		y := spineY(x, r.W, r.H, t)
		if x == 0 {
			spine.MoveTo(r.X, r.Y+y)
		} else {
			spine.LineTo(r.X+x, r.Y+y)
		}
	}
	if cur.Timeline == TimelineGold {
		p.StrokePath(&spine, 2.4, palette.Gold)
		return
	}
	p.StrokePath(&spine, 1.6, palette.Spine.Alpha(0.85))
}

// curveStrip draws all saved points (oldest on the left, newest on the right)
// and scrolls horizontally when there are more than curvePoints.
func (a *App) curveStrip(c *ui.Context, points []journey.TimelinePoint) {
	n := len(points)
	if n == 0 {
		return
	}
	shown := make([]journey.TimelinePoint, n)
	for i := range n {
		shown[i] = points[n-1-i]
	}
	scope := a.timelineScopeID
	if a.curveScrollScope != scope {
		a.curveScrollScope = scope
		a.curveCount = 0
		a.curveScroll.X = math.MaxFloat32
	} else if n > a.curveCount && (a.curveScroll.MaxX == 0 || a.curveScroll.X >= a.curveScroll.MaxX-4) {
		a.curveScroll.X = math.MaxFloat32
	}
	a.curveCount = n
	still := c.Preferences().ReduceMotion
	a.settleGlow(c, still)
	still = still || cur.Timeline == TimelinePlain
	box := ui.Box(c).Height(curveHeight).Radius(radCard).Background(palette.Canvas).Border(1, palette.CardBorder).Clip()
	box.Children(func() {
		ui.ScrollHorizontal(c.Key("curve-" + scope)).TrackScroll(&a.curveScroll).Fill().Children(func() {
			strip := ui.Row(c).Height(curveHeight).Grow(1)
			if n > curvePoints {
				strip.MinWidth(float32(n) * curvePointWidth).Shrink(0)
			}
			strip.Draw(func(p *ui.Painter, r ui.Rect) {
				t := float32(0)
				if !still {
					t = float32(float64(p.Now().UnixMilli()%3_600_000)/1000) * 0.54
					p.After(33 * time.Millisecond)
				}
				paintHairs(p, r, t)
				for i, pt := range shown {
					px := r.W * (float32(i) + 0.5) / float32(n)
					x, y := r.X+px, r.Y+spineY(px, r.W, r.H, t)
					stem := r.Y + curveBand
					if i%2 == 1 {
						stem = r.Y + r.H - curveBand
					}
					p.Line(x, y, x, stem, 1, palette.Strand.Alpha(0.6))
					if pointKey(pt) == a.glow {
						k := min(float32(p.Now().Sub(a.glowAt))/float32(glowDelay), 1)
						for j, rad := range []float32{30, 20, 12} {
							p.FillPath((&ui.Path{}).Circle(x, y, rad*(0.5+0.5*k)), palette.Gold.Alpha(0.18*float32(j+1)*k))
						}
					}
					p.FillPath((&ui.Path{}).Circle(x, y, 5.5), palette.Node)
					p.StrokePath((&ui.Path{}).Circle(x, y, 5.5), 1.4, palette.Spine.Alpha(0.9))
					p.FillPath((&ui.Path{}).Circle(x, y, 1.8), palette.NodeCore)
				}
			})
			strip.Children(func() {
				for i, pt := range shown {
					col := ui.Column(c.Key(pointKey(pt))).Grow(1).Basis(0).FillHeight().AlignItems(ui.Center).Padding(12, 6)
					if n > curvePoints {
						col.MinWidth(curvePointWidth)
					}
					if i%2 == 0 {
						col.Justify(ui.Start)
					} else {
						col.Justify(ui.End)
					}
					col.Children(func() { a.curveCard(c, pt) })
				}
			})
		})
	})
}

func (a *App) curveCard(c *ui.Context, pt journey.TimelinePoint) {
	id := pointKey(pt)
	card := ui.Column(c).Gap(2).Padding(spInside, spItem).Radius(radPanel).Background(palette.Card).Border(1, palette.CardBorder).
		Shadow(0, 4, 14, 0, palette.Shadow.Alpha(0.06)).Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(when(pt.CreatedAt, c.Now()))
	if a.glow == id {
		card.Border(1, palette.Gold).Shadow(0, 0, 18, 0, palette.Gold.Alpha(0.7))
	}
	card.Draw(func(p *ui.Painter, r ui.Rect) { // the hover is only painted
		if hover, _ := over(card); hover {
			p.Stroke(r, palette.Gold, radPanel, 1.5)
		}
	})
	card.OnClick(func() { a.lightPoint(id) }).Children(func() {
		ui.Text(c, when(pt.CreatedAt, c.Now())).FontSize(fsSub).Bold().MaxLines(1)
		muted(ui.Text(c, tierShort(pt.Scope.Tier))).MaxLines(1)
		muted(ui.Text(c, pointLocations(pt))).MaxLines(2)
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
