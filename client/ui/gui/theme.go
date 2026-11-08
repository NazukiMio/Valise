package gui

import "github.com/egoist/mygo/ui"

// Palette of the desktop UI (docs/UI.md 7.1): cream paper, white cards and a
// gold glow, taken from the user's reference page. The dark theme is decided
// after the sketches; until then the light palette is used in both.
var palette = struct {
	Paper, Card, CardBorder, Text, TextSub, Gold, GoldSoft, Line, Ok, Warn, Stop, Board, Side, SideText ui.Color
}{
	Paper:      ui.Hex("#f7f6f3"),
	Card:       ui.RGBA(255, 255, 255, 0.96),
	CardBorder: ui.RGBA(220, 215, 206, 0.7),
	Text:       ui.Hex("#2b2825"),
	TextSub:    ui.Hex("#8c857b"),
	Gold:       ui.RGBA(212, 192, 163, 0.9),
	GoldSoft:   ui.RGBA(245, 230, 205, 0.95),
	Line:       ui.Hex("#dfdad0"),
	Ok:         ui.Hex("#5f7d5a"),
	Warn:       ui.Hex("#a06a2c"),
	Stop:       ui.Hex("#8a8377"),
	Board:      ui.Hex("#fcfbf8"),
	Side:       ui.Hex("#efece6"),
	SideText:   ui.Hex("#8c857b"),
}

// displayFont is for page titles only: a Song face where the system has one.
const displayFont = "Songti SC, Noto Serif CJK SC, Noto Serif SC, Source Han Serif SC"

// theme is the widgets' theme built on the palette.
func theme(base *ui.Theme) *ui.Theme {
	t := *base
	t.Dark = false
	t.Background = palette.Paper
	t.Surface = ui.Hex("#ffffff")
	t.SurfaceHover = ui.Hex("#f4f2ee")
	t.SurfacePressed = ui.Hex("#f0ede6")
	t.Border = ui.Hex("#dfdcd5")
	t.Text = palette.Text
	t.TextMuted = palette.TextSub
	t.Accent = ui.Hex("#70685d")
	t.AccentHover = ui.Hex("#5e574d")
	t.AccentPressed = ui.Hex("#4d473f")
	t.AccentText = ui.Hex("#ffffff")
	t.Focus = ui.Hex("#cfc4b5")
	t.Radius = 8
	return &t
}

// card styles a white card with a thin border.
func card(e ui.Element) ui.Element {
	return e.Padding(18).Gap(12).Radius(14).Background(palette.Card).
		Border(1, palette.CardBorder).Shadow(0, 1, 3, 0, ui.RGBA(62, 54, 44, 0.05))
}

// muted is secondary text.
func muted(e ui.Element) ui.Element { return e.TextColor(palette.TextSub).FontSize(12) }
