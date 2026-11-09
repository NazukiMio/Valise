package gui

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

// attentionCard is one thing to handle, with its two buttons (docs/UI.md
// 7.3): “带我去处理” opens the page that handles it, “知道了” stops it until
// the situation changes. The first card's step is the page's one primary
// button.
func (a *App) attentionCard(c *ui.Context, it attention, first bool) {
	ui.Row(c).Padding(16, 18).Gap(14).Radius(14).Background(palette.AlertBg).Border(1, palette.AlertBorder).
		AlignItems(ui.Center).Children(func() {
		ui.Box(c).Size(10, 10).Radius(5).Background(palette.Warn).Shadow(0, 0, 8, 1, palette.Warn.Alpha(0.35))
		ui.Column(c).Grow(1).Gap(4).Children(func() {
			ui.Text(c, it.Title).FontSize(15).Bold().Wrap()
			ui.Text(c, it.Reason).FontSize(13).TextColor(palette.AlertText).Wrap()
		})
		ui.Row(c).Gap(8).Children(func() {
			ui.Button(c, T("reminder.ok")).OnClick(func() { a.dismiss(it) })
			var b ui.Element
			if first {
				b = ui.PrimaryButton(c, T("reminder.go"))
			} else {
				b = ui.Button(c, T("reminder.go"))
			}
			b.OnClick(func() { a.goHandle(it) })
		})
	})
}

// knownCard is a reminder already acknowledged: quiet, and it can be asked
// back.
func (a *App) knownCard(c *ui.Context, it attention) {
	ui.Row(c).Padding(12, 18).Gap(14).Radius(12).Background(palette.Board).Border(1, palette.CardBorder).
		AlignItems(ui.Center).Children(func() {
		ui.Icon(c, icons.check).FontSize(16).TextColor(palette.TextSub)
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			ui.Text(c, it.Title).FontSize(13).Wrap()
			muted(ui.Text(c, T("reminder.known")))
		})
		ui.Button(c, T("reminder.again")).OnClick(func() { a.undismiss(it) })
		ui.Button(c, T("reminder.go")).OnClick(func() { a.goHandle(it) })
	})
}

// dismiss remembers this reminder in its current state, on this computer.
func (a *App) dismiss(it attention) {
	a.prefs.Dismissed[it.Key] = it.State
	a.savePrefs()
}

func (a *App) undismiss(it attention) {
	delete(a.prefs.Dismissed, it.Key)
	a.savePrefs()
}

// goHandle opens the page that handles a reminder.
func (a *App) goHandle(it attention) {
	if strings.HasPrefix(it.Go, "/agents/") {
		a.agentTab, a.point = it.Tab, ""
	}
	a.router.Push(it.Go)
}
