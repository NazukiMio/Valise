package gui

import (
	"github.com/egoist/mygo/ui"
)

// banners are the bars over every page, outside the theme's layout: work
// that goes on while the person looks elsewhere, and what a theme or a
// language file could not do. They share one look so a theme cannot hide
// one of them, and they come and go with a short slide.
func (a *App) banners(c *ui.Context) {
	ui.Column(c).AlignItems(ui.Stretch).Gap(0).Children(func() {
		a.recoveryBanner(c.Key("b-recover"))
		a.nativeBanner(c.Key("b-native"))
		a.restoreBanner(c.Key("b-restore"))
		a.restoreTestBanner(c.Key("b-test"))
		a.appearanceWarnings(c.Key("b-look"))
	})
}

// banner is one bar: a mark for its tone, one sentence, and its buttons
// at the end. "busy" is work going on; "warn" and "danger" are as anywhere.
func banner(c *ui.Context, tone string, text func(), actions func()) {
	bg, border := toneGround(tone)
	ink := toneInk(tone)
	if tone == "busy" {
		bg, border, ink = palette.Board, palette.CardBorder, palette.Brand
	}
	ui.Row(c).Padding(spInside+2, spPage-8).Gap(spItem).AlignItems(ui.Center).Background(bg).
		BorderWidth(0, 0, 1, 0).BorderColor(border).
		Transition(ui.ElementTransition{Duration: motionBase, Enter: &ui.Motion{Y: -6}, Exit: &ui.Motion{Collapse: true}}).
		Children(func() {
			if tone == "busy" {
				ui.Spinner(c).Size(14, 14)
			} else {
				ui.Icon(c, icons.alert).FontSize(fsTitle3).TextColor(ink)
			}
			ui.Column(c).Grow(1).Gap(2).Children(text)
			if actions != nil {
				ui.Row(c).Gap(spInside).Shrink(0).AlignItems(ui.Center).Children(actions)
			}
		})
}

// bannerText is a banner's sentence, with an optional quieter second part.
func bannerText(c *ui.Context, main, sub string) func() {
	return func() {
		ui.Text(c, main).FontSize(fsSub).Bold().Wrap()
		if sub != "" {
			muted(ui.Text(c, sub)).FontSize(fsCaption).Wrap()
		}
	}
}
