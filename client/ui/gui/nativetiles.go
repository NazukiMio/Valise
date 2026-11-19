package gui

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"valise/core/install"
	"valise/ui/service"
)

// nativeDailyCard is everyday use: talking to the agent, as a tile. A tile is the whole choice, so the
// page keeps its one primary button for the setup wizard below.
func (a *App) nativeDailyCard(c *ui.Context, server, instance string, info *install.NativeInfo) {
	card(ui.Column(c)).Gap(spItem).Children(func() {
		sectionTitle(ui.Text(c, T("native.daily_title")))
		ui.Row(c).Gap(spItem).AlignItems(ui.Stretch).Children(func() {
			if len(info.SessionArgv) > 0 {
				nativeTile(c.Key("session"), icons.chat, T("native.session"), T("native.session_hint"), func() {
					a.startNative(server, instance, service.NativeSession, false)
				})
			}
		})
		if len(info.SessionArgv) > 0 {
			ui.Collapsible(c, T("native.session_command"), a.fold("native-session-command"), func() {
				mono(ui.Text(c, strings.Join(info.SessionArgv, " "))).Wrap()
			})
		}
	})
}

// nativeTile is one way in: its mark, what it opens and one sentence.
func nativeTile(c *ui.Context, icon *ui.SVG, title, hint string, open func()) {
	t := ui.Row(c).Grow(1).Basis(0).Gap(spItem).Padding(spGroup).Radius(radPanel).
		Background(palette.Board).Border(1, palette.CardBorder).AlignItems(ui.Start).OnClick(open)
	pressable(t, title, radPanel).Children(func() {
		ui.Box(c).Size(36, 36).Radius(radControl + 4).Background(palette.Brand.Alpha(0.12)).Justify(ui.Center).AlignItems(ui.Center).
			Shrink(0).Children(func() { ui.Icon(c, icon).FontSize(fsTitle2).TextColor(palette.Brand) })
		ui.Column(c).Grow(1).Gap(spHair).Children(func() {
			ui.Text(c, title).FontSize(fsBody).Bold().Wrap()
			muted(ui.Text(c, hint)).FontSize(fsSub).Wrap()
		})
		chevron(c)
	})
}
