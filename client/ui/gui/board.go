package gui

import "github.com/egoist/mygo/ui"

// cell is one square of a status board: what it is about, what is true, and
// how that reads (ok, stop, warn, "" for neutral).
type cell struct{ Label, Value, Tone string }

// statusBoard shows several facts side by side, one cell each, as a
// departure board does, so that no two states merge into one badge
// (docs/UI.md 7.1, AGENTS.md 7.5).
func statusBoard(c *ui.Context, cells []cell, compact bool) {
	pad := float32(14)
	if compact {
		pad = 10
	}
	ui.Row(c).Radius(10).Background(palette.Board).Border(1, palette.CardBorder).Clip().Children(func() {
		for i, x := range cells {
			col := ui.Column(c.Key(x.Label)).Grow(1).Basis(0).Gap(3).Padding(pad-2, pad)
			if i > 0 {
				col.BorderWidth(0, 0, 0, 1).BorderColor(palette.CardBorder)
			}
			col.Children(func() {
				ui.Text(c, x.Label).FontSize(11).TextColor(palette.TextSub).LetterSpacing(0.6)
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					if x.Tone != "" {
						ui.Box(c).Size(6, 6).Radius(3).Background(toneColor(x.Tone))
					}
					v := ui.Text(c, x.Value).FontSize(13).MaxLines(1)
					if x.Tone == "warn" {
						v.TextColor(palette.Warn)
					}
				})
			})
		}
	})
}

func toneColor(tone string) ui.Color {
	switch tone {
	case "ok":
		return palette.Ok
	case "warn":
		return palette.Warn
	}
	return palette.Stop
}
