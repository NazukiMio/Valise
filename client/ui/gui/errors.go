package gui

import (
	"errors"

	"github.com/egoist/mygo/ui"

	"valise/core/valise"
)

// errorBox shows an error as reason + suggestion, details folded away
// (docs/UI.md 5). open keeps the fold's state between frames.
func errorBox(c *ui.Context, err error, open *bool) {
	reason, suggestion, detail := text.errUnexpected, "", err.Error()
	var ue *valise.UserError
	if errors.As(err, &ue) {
		reason, suggestion, detail = ue.Reason, ue.Suggestion, ue.Detail
		if detail == "" {
			detail = ue.Code
		} else {
			detail = ue.Code + "\n" + detail
		}
	}
	ui.Column(c).Padding(14, 16).Gap(6).Radius(10).Background(palette.ErrorBg).
		Border(1, palette.ErrorBorder).Children(func() {
		ui.Text(c, reason).Bold().TextColor(palette.Warn)
		if suggestion != "" {
			ui.Text(c, suggestion).TextColor(palette.Text)
		}
		if detail != "" {
			ui.Collapsible(c, text.techInfo, open, func() {
				muted(ui.Text(c, detail)).Font(cur.Fonts.Mono)
			})
		}
	})
}
