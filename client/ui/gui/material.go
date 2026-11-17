package gui

import (
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/shader"
)

// Materials (docs/UI.md 7.4.4) are what a theme's surfaces are made of,
// beyond flat colors: the window's backdrop, how a card sits on it, and a
// mark under page titles. They are named variants a theme picks; views
// never test which one is current. Every material keeps text, warnings,
// errors and dialogs on solid ground: nothing is drawn over them.
const (
	MaterialFlat    = "flat"    // the classic themes: plain paper
	MaterialJournal = "journal" // paper grain and a dot grid, cards like stuck-in notes
	MaterialNight   = "night"   // a night sky that slowly twinkles, cards like lit panes
)

// Material is a theme's material and its few numbers.
type Material struct {
	Kind string
	// Grain and Grid are the journal's paper: how strong the grain is
	// (0..1) and the dot grid's spacing in DIPs (0 for none).
	Grain, Grid float32
	// Stars is how many stars per 10,000 square DIPs the night sky shows.
	Stars float32
}

// paperFx is the journal's paper: fibrous grain and a faint dot grid. It
// does not move, so the window paints it like any background.
var paperFx = shader.MustCompile(`package main

var Ink vec4
var Grain float
var Grid float

func hash(p vec2) float {
	return fract(sin(dot(p, vec2(127.1, 311.7))) * 43758.5453)
}

func noise(p vec2) float {
	i := floor(p)
	f := fract(p)
	u := f * f * (3 - 2*f)
	a := hash(i)
	b := hash(i + vec2(1, 0))
	c := hash(i + vec2(0, 1))
	d := hash(i + vec2(1, 1))
	return mix(mix(a, b, u.x), mix(c, d, u.x), u.y)
}

func Fragment(dst vec4, pos vec2) vec4 {
	fiber := noise(pos*vec2(0.9, 0.18)) * 0.6 + noise(pos*0.55) * 0.4
	speck := step(0.985, hash(floor(pos*0.5)))
	a := (fiber - 0.5) * 0.10 * Grain + speck * 0.05 * Grain
	if Grid > 0 {
		g := mod(pos, Grid) - Grid/2
		d := length(g)
		a += (1 - smoothstep(0.6, 1.4, d)) * 0.16
	}
	a = clamp(a, 0, 1)
	return vec4(Ink.rgb*a, a)
}
`)

// skyFx is the night sky: a deep gradient with a glow low on one side and
// stars that twinkle with Time. With less motion asked for, Time stays 0.
var skyFx = shader.MustCompile(`package main

var Glow vec4
var Star vec4
var Density float
var Time float

func hash(p vec2) float {
	return fract(sin(dot(p, vec2(41.3, 289.1))) * 15731.743)
}

func Fragment(dst vec4, pos vec2) vec4 {
	size := rectSize()
	uv := pos / size
	g := 1 - smoothstep(0, 0.9, distance(uv, vec2(0.85, 1.05)))
	h := 1 - smoothstep(0, 0.7, distance(uv, vec2(0.1, -0.15)))
	col := Glow.rgb*g*0.30 + Glow.rgb*h*0.12
	a := g*0.30 + h*0.12
	cell := floor(pos / 9)
	r := hash(cell)
	if r < Density {
		center := (cell + vec2(hash(cell+3.1), hash(cell+7.7))) * 9
		d := distance(pos, center)
		tw := 0.55 + 0.45*sin(Time*(0.6+r*2.4)+r*40)
		s := (1 - smoothstep(0, 1.1+r*12, d)) * tw * (0.35 + 0.65*hash(cell+1.3))
		col += Star.rgb * s
		a += s
	}
	a = clamp(a, 0, 1)
	return vec4(col, a)
}
`)

// backdrop paints the theme's material behind everything, then its
// picture, if it has one.
func backdrop(p *ui.Painter, r ui.Rect) {
	m := cur.Material
	switch m.Kind {
	case MaterialJournal:
		paperFx.Draw(p, r, shader.Options{Uniforms: map[string]any{
			"Ink": palette.Shadow, "Grain": m.Grain, "Grid": m.Grid}})
	case MaterialNight:
		t := float32(0)
		if !p.Preferences().ReduceMotion {
			t = float32(p.Now().UnixMilli()%600000) / 1000
			p.AnimationFrameRate(12) // a slow twinkle needs few frames
		}
		skyFx.Draw(p, r, shader.Options{Uniforms: map[string]any{
			"Glow": palette.Strand, "Star": palette.Text, "Density": m.Stars / 100 * 0.81, "Time": t}})
	}
	if img := cur.Images["backdrop"]; img != nil {
		p.Image(img, r, ui.Cover)
	}
}

// hasBackdrop reports whether the window paints anything behind its pages.
func hasBackdrop() bool {
	return cur.Material.Kind == MaterialJournal || cur.Material.Kind == MaterialNight || cur.Images["backdrop"] != nil
}

// materialCard is how a card sits on the material: a stuck-in note casts a
// short hard shadow; a lit pane has a bright upper edge.
func materialCard(e ui.Element) ui.Element {
	switch cur.Material.Kind {
	case MaterialJournal:
		return e.Shadow(0, 1, 0, 0, palette.Shadow.Alpha(0.10)).Shadow(2, 3, 0, 0, palette.Shadow.Alpha(0.05)).
			Shadow(0, 10, 24, -10, palette.Shadow.Alpha(0.12))
	case MaterialNight:
		return e.InsetShadow(0, 1, 0, 0, palette.Text.Alpha(0.08)).Shadow(0, 12, 32, -12, palette.Shadow.Alpha(0.6))
	}
	return e.Shadow(0, 1, 2, 0, palette.Shadow.Alpha(0.06)).Shadow(0, 6, 18, -6, palette.Shadow.Alpha(0.07))
}

// titleMark is the mark under a page's title: a stroke of highlighter on
// the journal's paper, a thin rule of light at night, nothing elsewhere.
func titleMark(e ui.Element) ui.Element {
	switch cur.Material.Kind {
	case MaterialJournal:
		return e.Draw(func(p *ui.Painter, r ui.Rect) {
			h := r.H * 0.32
			p.Fill(ui.Rect{X: r.X - 4, Y: r.Y + r.H - h - 2, W: r.W + 8, H: h}, palette.Gold.Alpha(0.38), 2)
		})
	case MaterialNight:
		return e.Draw(func(p *ui.Painter, r ui.Rect) {
			p.FillGradient(ui.Rect{X: r.X, Y: r.Y + r.H + 2, W: min(r.W, 160), H: 1.5}, ui.LinearGradient{
				Angle: 90, From: palette.Brand, To: palette.Brand.Alpha(0)}, 1)
		})
	}
	return e
}
