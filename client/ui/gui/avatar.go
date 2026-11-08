package gui

import (
	"github.com/egoist/mygo/ui"

	"valise/core/profile"
)

// avatar shows who an agent is (docs/UI.md 7.3): the picture the person
// chose for it on this computer, else the harness's official logo from its
// profile, else the sketch's line robot in the harness's color. Character
// art is for the TUI only.
func (a *App) avatar(c *ui.Context, r agentRow, side float32) {
	d := a.display[r.Profile]
	ink := palette.NodeCore
	if d != nil && d.Color != "" {
		ink = ui.Hex(d.Color)
	}
	box := ui.Box(c).Size(side, side).Radius(side / 4).Clip().Label(r.Name + " 的图标")
	if img := a.agentImage(r.Server, r.Instance); img != nil {
		box.Background(palette.Card).Border(1, palette.CardBorder).Children(func() {
			ui.Image(c, img).Size(side, side)
		})
		return
	}
	if logo := a.logo(r.Profile, d); logo != nil {
		box.Background(palette.LogoBg).Border(1, palette.CardBorder).Center().Children(func() {
			ui.Image(c, logo).Size(side*0.66, side*0.66)
		})
		return
	}
	box.Background(ink.Alpha(0.12)).Border(1, ink.Alpha(0.2)).Center().Children(func() {
		ui.Icon(c, icons.agent).FontSize(side * 0.56).TextColor(ink)
	})
}

// logo decodes a profile's official SVG once.
func (a *App) logo(id string, d *profile.Display) ui.ImageSource {
	if d == nil || d.Logo == nil || d.Logo.SVG == "" {
		return nil
	}
	if img, ok := a.logos[id]; ok {
		return img
	}
	img, err := ui.DecodeImage([]byte(d.Logo.SVG))
	if err != nil {
		img = nil
	}
	a.logos[id] = img
	return img
}

// iconChoice lets the person pick a picture for this agent on this
// computer, or go back to the default.
func (a *App) iconChoice(c *ui.Context, row *agentRow) {
	if a.pickImage == nil {
		return
	}
	textLink(c, "更换图标", func() {
		path, err := a.pickImage()
		if err == nil && path != "" {
			err = a.setAgentImage(row.Server, row.Instance, path)
		}
		a.iconErr = err
	})
	if a.agentImage(row.Server, row.Instance) != nil {
		textLink(c, "恢复默认", func() { a.clearAgentImage(row.Server, row.Instance); a.iconErr = nil })
	}
}
