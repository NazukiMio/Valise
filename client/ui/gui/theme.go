package gui

import "github.com/egoist/mygo/ui"

// Themes (docs/UI.md 7.4): every color, font and size a view uses comes
// from the current theme; views write none of their own.

// Palette is a theme's colors. Colors used with transparency in drawing
// (Strand, Spine, Gold, Shadow) are given opaque here and faded where drawn.
type Palette struct {
	Paper, Card, CardBorder, Text, TextSub, Line, Board, Hover ui.Color
	Ok, Warn, Stop                                             ui.Color
	Side, SideText, Brand                                      ui.Color
	Accent, AccentHover, AccentText, Surface, SurfaceHover     ui.Color
	Border, Focus                                              ui.Color
	Gold, GoldSoft, Canvas, Strand, Spine, Node, NodeCore      ui.Color
	AlertBg, AlertBorder, AlertText                            ui.Color
	ErrorBg, ErrorBorder, TroubleBg, TroubleBorder             ui.Color
	LogoBg, Shadow                                             ui.Color
}

// colorFields names the palette's colors as a theme file writes them.
func colorFields(p *Palette) map[string]*ui.Color {
	return map[string]*ui.Color{
		"paper": &p.Paper, "card": &p.Card, "card_border": &p.CardBorder, "text": &p.Text, "text_sub": &p.TextSub,
		"line": &p.Line, "board": &p.Board, "hover": &p.Hover, "ok": &p.Ok, "warn": &p.Warn, "stop": &p.Stop,
		"side": &p.Side, "side_text": &p.SideText, "brand": &p.Brand,
		"accent": &p.Accent, "accent_hover": &p.AccentHover, "accent_text": &p.AccentText,
		"surface": &p.Surface, "surface_hover": &p.SurfaceHover, "border": &p.Border, "focus": &p.Focus,
		"gold": &p.Gold, "gold_soft": &p.GoldSoft, "canvas": &p.Canvas, "strand": &p.Strand, "spine": &p.Spine,
		"node": &p.Node, "node_core": &p.NodeCore,
		"alert_bg": &p.AlertBg, "alert_border": &p.AlertBorder, "alert_text": &p.AlertText,
		"error_bg": &p.ErrorBg, "error_border": &p.ErrorBorder, "trouble_bg": &p.TroubleBg, "trouble_border": &p.TroubleBorder,
		"logo_bg": &p.LogoBg, "shadow": &p.Shadow,
	}
}

// Fonts are a theme's families; "" is the system's.
type Fonts struct {
	Text    string `json:"text"`
	Display string `json:"display"` // page titles and the verdict
	Mono    string `json:"mono"`
}

// Sizes are a theme's numbers, in DIPs.
type Sizes struct {
	Radius     float32 `json:"radius"`      // controls
	CardRadius float32 `json:"card_radius"` // cards and panels
	Verdict    float32 `json:"verdict"`     // the home page's sentence
	Title      float32 `json:"title"`       // page titles
	Text       float32 `json:"text"`        // body text
}

// Timeline variants (docs/UI.md 7.4).
const (
	TimelineStrands = "strands" // the flowing hair strands
	TimelineGold    = "gold"    // one breathing gold line
	TimelinePlain   = "plain"   // a still, straight line
)

// Theme is one complete look: tokens, variants, layout and pictures.
type Theme struct {
	ID, Name string
	Dark     bool
	Pal      Palette
	Fonts    Fonts
	Sizes    Sizes
	Timeline string
	Shell    *Node // the window: navigation and the page area
	Home     *Node // the home page
	Images   map[string]ui.ImageSource
	// Notes say what Valise put back that the theme left out.
	Notes []string
}

// The current theme's tokens, read by every view on the main thread.
var (
	cur     = classicLight()
	palette = cur.Pal
)

// use makes t the current theme.
func use(t *Theme) {
	cur, palette = t, t.Pal
}

func classicFonts() Fonts {
	return Fonts{Display: "Songti SC, Noto Serif CJK SC, Noto Serif SC, Source Han Serif SC", Mono: "monospace"}
}

func classicSizes() Sizes { return Sizes{Radius: 8, CardRadius: 14, Verdict: 36, Title: 30, Text: 0} }

// classicLight is the palette of docs/UI.md 7.1: cream paper, white cards
// and a gold glow, from the user's reference page.
func classicLight() *Theme {
	return &Theme{ID: ThemeLight, Name: "经典浅色", Fonts: classicFonts(), Sizes: classicSizes(), Timeline: TimelineStrands,
		Shell: classicShell(), Home: classicHome(), Pal: Palette{
			Paper: ui.Hex("#f7f6f3"), Card: ui.RGBA(255, 255, 255, 0.96), CardBorder: ui.RGBA(220, 215, 206, 0.7),
			Text: ui.Hex("#2b2825"), TextSub: ui.Hex("#8c857b"), Line: ui.Hex("#dfdad0"), Board: ui.Hex("#fcfbf8"), Hover: ui.Hex("#fbfaf6"),
			Ok: ui.Hex("#5f7d5a"), Warn: ui.Hex("#a06a2c"), Stop: ui.Hex("#8a8377"),
			Side: ui.Hex("#efece6"), SideText: ui.Hex("#8c857b"), Brand: ui.Hex("#9a7b4f"),
			Accent: ui.Hex("#70685d"), AccentHover: ui.Hex("#5e574d"), AccentText: ui.Hex("#ffffff"),
			Surface: ui.Hex("#ffffff"), SurfaceHover: ui.Hex("#f4f2ee"), Border: ui.Hex("#dfdcd5"), Focus: ui.Hex("#cfc4b5"),
			Gold: ui.Hex("#d4c0a3"), GoldSoft: ui.RGBA(245, 230, 205, 0.95), Canvas: ui.Hex("#fbf9f4"),
			Strand: ui.Hex("#b09c82"), Spine: ui.Hex("#a89680"), Node: ui.Hex("#ffffff"), NodeCore: ui.Hex("#8c7e6c"),
			AlertBg: ui.Hex("#fbf3e6"), AlertBorder: ui.RGBA(214, 178, 128, 0.45), AlertText: ui.Hex("#6b5a44"),
			ErrorBg: ui.Hex("#fbf4e8"), ErrorBorder: ui.RGBA(220, 190, 150, 0.6),
			TroubleBg: ui.Hex("#fffaf2"), TroubleBorder: ui.RGBA(214, 150, 90, 0.7),
			LogoBg: ui.Hex("#ffffff"), Shadow: ui.Hex("#3e362c"),
		}}
}

// classicDark is the same hues at night.
func classicDark() *Theme {
	t := classicLight()
	t.ID, t.Name, t.Dark = ThemeDark, "经典深色", true
	t.Pal = Palette{
		Paper: ui.Hex("#1f1d1a"), Card: ui.Hex("#2a2723"), CardBorder: ui.RGBA(255, 240, 220, 0.09),
		Text: ui.Hex("#ece7df"), TextSub: ui.Hex("#a39b8f"), Line: ui.Hex("#45403a"), Board: ui.Hex("#24221f"), Hover: ui.Hex("#302d29"),
		Ok: ui.Hex("#94b28b"), Warn: ui.Hex("#dba45f"), Stop: ui.Hex("#948d82"),
		Side: ui.Hex("#191816"), SideText: ui.Hex("#a39b8f"), Brand: ui.Hex("#c9a66f"),
		Accent: ui.Hex("#d8cbb6"), AccentHover: ui.Hex("#e6dbc9"), AccentText: ui.Hex("#1f1d1a"),
		Surface: ui.Hex("#2f2c28"), SurfaceHover: ui.Hex("#37332e"), Border: ui.Hex("#45403a"), Focus: ui.Hex("#6b5f4f"),
		Gold: ui.Hex("#c9b08a"), GoldSoft: ui.Hex("#3a3226"), Canvas: ui.Hex("#23211e"),
		Strand: ui.Hex("#c4b092"), Spine: ui.Hex("#c8b69c"), Node: ui.Hex("#2a2723"), NodeCore: ui.Hex("#d4c0a3"),
		AlertBg: ui.Hex("#382d20"), AlertBorder: ui.RGBA(219, 164, 95, 0.35), AlertText: ui.Hex("#d8c3a5"),
		ErrorBg: ui.Hex("#382d20"), ErrorBorder: ui.RGBA(219, 164, 95, 0.4),
		TroubleBg: ui.Hex("#302820"), TroubleBorder: ui.RGBA(219, 164, 95, 0.55),
		LogoBg: ui.Hex("#f4f1ec"), Shadow: ui.Hex("#000000"),
	}
	return t
}

// widgets is mygo's widget theme built on the current palette.
func widgets(base *ui.Theme) *ui.Theme {
	t, p := *base, palette
	t.Dark = cur.Dark
	t.Background, t.Surface, t.SurfaceHover = p.Paper, p.Surface, p.SurfaceHover
	t.SurfacePressed = p.SurfaceHover.Mix(p.Border, 0.3)
	t.Border, t.Text, t.TextMuted = p.Border, p.Text, p.TextSub
	t.Accent, t.AccentHover, t.AccentPressed, t.AccentText = p.Accent, p.AccentHover, p.AccentHover.Mix(p.Text, 0.2), p.AccentText
	t.Focus, t.Warning, t.Success = p.Focus, p.Warn, p.Ok
	t.Radius = cur.Sizes.Radius
	if cur.Fonts.Text != "" {
		t.Font = cur.Fonts.Text
	}
	if cur.Sizes.Text > 0 {
		t.FontSize = cur.Sizes.Text
	}
	return &t
}

// card styles a card with a thin border.
func card(e ui.Element) ui.Element {
	return e.Padding(18).Gap(12).Radius(cur.Sizes.CardRadius).Background(palette.Card).
		Border(1, palette.CardBorder).Shadow(0, 1, 3, 0, palette.Shadow.Alpha(0.05))
}

// muted is secondary text.
func muted(e ui.Element) ui.Element { return e.TextColor(palette.TextSub).FontSize(12) }

// mono is code-like text: paths, commands, ids.
func mono(e ui.Element) ui.Element { return e.Font(cur.Fonts.Mono) }
