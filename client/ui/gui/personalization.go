package gui

import "github.com/egoist/mygo/ui"

func (a *App) personalization(c *ui.Context) {
	card(ui.Column(c)).Gap(12).Children(func() {
		ui.Text(c, T("settings.appearance")).FontSize(16).Bold()
		a.themeSlot(c, &Node{})
	})
}

// Errors stay outside the theme's layout so moving its controls does not
// let a broken or incomplete theme hide its own diagnostics.
func (a *App) appearanceWarnings(c *ui.Context) {
	if a.themeErr == nil && a.langErr == nil && len(cur.Notes) == 0 && !words.Load().Imported {
		return
	}
	banner(c, "warn", func() {
		if a.themeErr != nil {
			ui.Text(c, a.themeErr.Error()).FontSize(fsSub).Wrap()
		}
		for i, note := range cur.Notes {
			ui.Text(c.Key(i), note).FontSize(fsSub).Wrap()
		}
		a.languageNotes(c)
	}, func() {
		ui.Button(c, T("settings.open_personalization")).OnClick(func() {
			a.setTab = 2
			a.navigate("/settings")
		})
	})
}
