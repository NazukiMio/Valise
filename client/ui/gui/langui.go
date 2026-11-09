package gui

import (
	"errors"
	"sort"

	"github.com/egoist/mygo/ui"
)

// The language group of the bottom menu (docs/UI.md 7.6): the built-in
// language, the imported catalogs and “导入语言…”.

func (a *App) languageItems(m *ui.Menu) {
	m.Separator()
	chosen := words.ID
	if m.Item(T("lang.item", "name", baseCatalog.Name)).Checked(chosen == baseCatalog.ID).Chosen() {
		a.setLocale("")
	}
	ids := make([]string, 0, len(a.locales))
	for id := range a.locales {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		e := a.locales[id]
		name := T("lang.unusable_item", "id", id)
		if e.cat != nil {
			name = T("lang.item_unofficial", "name", e.cat.Name)
		}
		if m.Item(name).Checked(chosen == id && e.cat != nil).Chosen() {
			a.setLocale(id)
		}
	}
	if a.pickFile != nil && m.Item(T("lang.import")).Chosen() {
		a.importLocaleChosen()
	}
}

// languageNotes says, under the menu, what is wrong with the chosen
// language, and that an imported one is not Valise's.
func (a *App) languageNotes(c *ui.Context) {
	if a.langErr != nil {
		ui.Text(c, a.langErr.Error()).FontSize(11).TextColor(palette.Warn).Wrap()
	}
	if words.Imported {
		note := T("lang.unofficial", "name", words.Name)
		if words.Missing > 0 {
			note += T("common.dot_sep") + T("lang.missing", "n", words.Missing)
		}
		muted(ui.Text(c, note)).FontSize(11).Wrap()
	}
}

func (a *App) importLocaleChosen() {
	a.run(func() {
		path, err := a.pickFile(T("lang.pick_title"), T("lang.pick_msg"))
		a.update(func() {
			if err != nil || path == "" {
				a.langErr = err
				return
			}
			id, err := a.importLocale(path)
			if err != nil {
				a.langErr = errors.New(T("lang.err.not_imported", "reason", err.Error()))
				return
			}
			a.loadLocales()
			a.setLocale(id)
		})
	})
}
