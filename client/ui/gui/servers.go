package gui

import "github.com/egoist/mygo/ui"

// serversPage lists the servers this computer knows (read only; adding and
// managing servers come with the server segment).
func (a *App) serversPage(c *ui.Context) {
	if !a.servers.started {
		a.refreshServers()
	}
	pageHead(c, text.serversTitle, text.serversSub)
	if a.servers.err != nil {
		errorBox(c, a.servers.err, a.fold("servers-err"))
	}
	rows := a.servers.value
	if len(rows) == 0 {
		if a.servers.busy {
			ui.Spinner(c)
		} else if a.servers.err == nil {
			muted(ui.Text(c, text.serversEmpty)).FontSize(14)
		}
		return
	}
	card(ui.Column(c)).Padding(6, 0).Gap(0).Children(func() {
		for i, r := range rows {
			if i > 0 {
				ui.Divider(c)
			}
			ui.Row(c.Key(r.Name)).Padding(14, 18).Gap(14).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, icons.server).FontSize(20).TextColor(palette.TextSub)
				ui.Column(c).Grow(1).Gap(3).Children(func() {
					ui.Text(c, r.Name).Bold()
					line := r.Kind
					if r.Address != "" {
						line += " · " + r.Address
					}
					muted(ui.Text(c, line))
				})
			})
		}
	})
}
