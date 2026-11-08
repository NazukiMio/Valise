package gui

import (
	"errors"
	"path/filepath"
	"sort"

	"github.com/egoist/mygo/ui"
)

// themeEntry is an imported theme: read once, kept with what was wrong.
type themeEntry struct {
	id    string
	theme *Theme
	err   error
}

var (
	builtinLight = classicLight()
	builtinDark  = classicDark()
)

// loadThemes reads every imported theme; one that fails stays listed with
// its reason.
func (a *App) loadThemes() {
	a.themes = map[string]themeEntry{}
	for _, id := range a.installedThemes() {
		t, _, err := readTheme(filepath.Join(a.themesDir(), id))
		if t != nil {
			t.ID = id
		}
		a.themes[id] = themeEntry{id: id, theme: t, err: err}
	}
}

// chooseTheme makes the chosen theme current; a theme that cannot be used
// falls back to following the system, and says why.
func (a *App) chooseTheme(sysDark bool) {
	system := builtinLight
	if sysDark {
		system = builtinDark
	}
	switch id := a.prefs.Theme; id {
	case "", ThemeSystem:
		use(system)
	case ThemeLight:
		use(builtinLight)
	case ThemeDark:
		use(builtinDark)
	default:
		e, ok := a.themes[id]
		switch {
		case !ok:
			a.themeErr = errors.New("找不到所选的主题，已改用跟随系统")
			use(system)
		case e.err != nil:
			a.themeErr = errors.New("主题“" + id + "”不能使用：" + e.err.Error() + "。已改用跟随系统")
			use(system)
		default:
			use(e.theme)
		}
	}
}

func (a *App) setTheme(id string) {
	a.prefs.Theme, a.themeErr = id, nil
	a.savePrefs()
}

// themeSlot is the theme menu, which every theme shows (docs/UI.md 7.4):
// the built-ins, the imported themes and “导入主题…”; under it, what was
// wrong or put back.
func (a *App) themeSlot(c *ui.Context, n *Node) {
	chosen := a.prefs.Theme
	if chosen == "" {
		chosen = ThemeSystem
	}
	label := "主题 · " + cur.Name
	if chosen == ThemeSystem {
		label = "主题 · 跟随系统"
	}
	if n.Style == "icon" {
		label = "主题"
	}
	pad := float32(8)
	if n.Style == "icon" {
		pad = 0 // a narrow rail: the button takes the whole width
	}
	n.box(ui.Column(c).Gap(6).Padding(0, pad)).Children(func() {
		b := ui.PopoverMenuButton(c, label, func(m *ui.Menu) {
			for _, b := range []struct{ id, name string }{{ThemeSystem, "跟随系统"}, {ThemeLight, "经典浅色"}, {ThemeDark, "经典深色"}} {
				if m.Item(b.name).Checked(chosen == b.id).Chosen() {
					a.setTheme(b.id)
				}
			}
			ids := make([]string, 0, len(a.themes))
			for id := range a.themes {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			if len(ids) > 0 {
				m.Separator()
			}
			for _, id := range ids {
				e := a.themes[id]
				name := id + "（不能使用）"
				if e.theme != nil {
					name = e.theme.Name
				}
				if m.Item(name).Checked(chosen == id).Chosen() {
					a.setTheme(id)
				}
			}
			if a.pickFolder != nil {
				m.Separator()
				if m.Item("导入主题…").Chosen() {
					a.importChosen()
				}
			}
		})
		if n.Style == "icon" {
			b.FontSize(12).Padding(6, 4).Label("主题 · " + cur.Name)
		}
		if a.themeErr != nil {
			ui.Text(c, a.themeErr.Error()).FontSize(11).TextColor(palette.Warn).Wrap()
		}
		for i, note := range cur.Notes {
			muted(ui.Text(c.Key(i), note)).FontSize(11).Wrap()
		}
	})
}

// importChosen asks for a theme folder, checks and copies it, and uses it.
func (a *App) importChosen() {
	dir, err := a.pickFolder()
	if err != nil || dir == "" {
		a.themeErr = err
		return
	}
	id, err := a.importTheme(dir)
	if err != nil {
		a.themeErr = errors.New("没有导入：" + err.Error())
		return
	}
	a.loadThemes()
	a.setTheme(id)
}
