package gui

import (
	"github.com/egoist/mygo/ui"
)

// View builds the window from the current theme's shell layout (docs/UI.md
// 7.4). Confirmations and the passphrase dialog are outside the layout:
// every theme gets them as the classic theme draws them.
func (a *App) View(c *ui.Context) {
	a.trackRoute()
	a.chooseTheme(c.Theme().Dark)
	c.SetTheme(widgets(c.Theme()))
	shell := *cur.Shell
	shell.Grow = max(shell.Grow, 1)
	root := ui.Column(c).Fill().AlignItems(ui.Stretch).Background(palette.Paper)
	if hasBackdrop() {
		root.Draw(backdrop)
	}
	root.Children(func() {
		a.banners(c)
		render(c, &shell, map[string]func(*ui.Context, *Node){
			"brand": a.brandSlot, "nav": a.navSlot, "tagline": taglineSlot, "page": a.pageSlot,
		})
	})
	a.keyDialog(c)
	a.passDialog(c)
}

func (a *App) brandSlot(c *ui.Context, n *Node) {
	ui.Row(c).Padding(0, 8, 8, 8).Gap(10).AlignItems(ui.Center).Children(func() {
		if img := cur.Images["brand"]; img != nil {
			ui.Image(c, img).Size(28, 28)
		} else {
			ui.Icon(c, icons.valise).FontSize(26).TextColor(palette.Brand)
		}
		if n.Style != "icon" {
			ui.Text(c, "Valise").FontSize(22).Bold().TextColor(palette.Text).Font(cur.Fonts.Display)
		}
	})
}

func taglineSlot(c *ui.Context, n *Node) {
	muted(ui.Text(c, T("shell.tagline"))).FontSize(fsCaption).Padding(0, 8)
}

// navItem is one first-level entry.
type navItem struct {
	path  string
	icon  *ui.SVG
	label string
}

func navItems() []navItem {
	return []navItem{{"/agents", icons.agent, T("nav.agents")}, {"/servers", icons.server, T("nav.servers")},
		{"/locations", icons.folder, T("nav.locations")}, {"/settings", icons.settings, T("nav.settings")}}
}

// section is the first-level entry the page belongs to.
func (a *App) section() string {
	page := a.router.Path()
	for _, it := range navItems() {
		if page == it.path || len(page) > len(it.path) && page[:len(it.path)+1] == it.path+"/" {
			return it.path
		}
	}
	return page
}

func (a *App) navigate(path string) {
	a.router.Push(path)
	a.trackRoute()
	if path == "/servers" && !a.servers.started {
		a.refreshServers()
	}
}

// navSlot is the navigation in one of its variants: the sidebar list, a
// narrow rail of icons, or a bar along the top. Every variant has the same
// entries.
func (a *App) navSlot(c *ui.Context, n *Node) {
	page := a.section()
	switch n.Style {
	case "rail", "top":
		var e ui.Element
		if n.Style == "rail" {
			e = ui.Column(c).Gap(6).AlignItems(ui.Center)
		} else {
			e = ui.Row(c).Gap(4).AlignItems(ui.Center)
		}
		n.box(e).Children(func() {
			for _, it := range navItems() {
				a.navButton(c.Key(it.path), it, page == it.path, n.Style == "top")
			}
		})
	default:
		// The chosen entry is a card on the side's paper, so it reads as
		// chosen in every theme, not as a pressed button.
		t := *c.Theme()
		t.SurfacePressed = palette.Card
		changed := false
		ui.Themed(c, &t).Background(ui.Transparent).AlignItems(ui.Stretch).Grow(max(n.Grow, 0)).Children(func() {
			changed = n.box(ui.Sidebar(c, &page, func() {
				for _, it := range navItems() {
					ui.SidebarItem(c, it.path, it.icon, it.label)
				}
			})).Background(ui.Transparent).TextColor(palette.SideText).Changed()
		})
		if changed {
			a.navigate(page)
		}
	}
}

func (a *App) navButton(c *ui.Context, it navItem, on, withText bool) {
	b := ui.Row(c).Gap(6).Padding(8, 10).Radius(cur.Sizes.Radius).AlignItems(ui.Center).Cursor(ui.CursorPointer).Role(ui.RoleButton).
		Label(it.label).Selected(on)
	ink := palette.SideText
	if on {
		b.Background(palette.Card)
		ink = palette.Text
	} else {
		b.Draw(func(p *ui.Painter, r ui.Rect) {
			if hover, _ := over(b); hover {
				p.Fill(r, palette.Hover, cur.Sizes.Radius)
			}
		})
	}
	b.OnClick(func() { a.navigate(it.path) }).Children(func() {
		ui.Icon(c, it.icon).FontSize(18).TextColor(ink)
		if withText {
			ui.Text(c, it.label).FontSize(13).TextColor(ink)
		}
	})
}

// pageSlot is the page area: it scrolls, and the router draws the page.
func (a *App) pageSlot(c *ui.Context, n *Node) {
	look := Node{Background: n.Background, Image: n.Image, Radius: n.Radius}
	look.box(ui.Scroll(c).Grow(max(n.Grow, 1))).Children(func() {
		col := ui.Column(c).Gap(pageGap)
		if n.Padding == nil {
			col.Padding(36, 44)
		}
		inner := *n
		inner.Grow, inner.Background, inner.Image, inner.Radius = 0, "", "", 0
		inner.box(col).Children(func() {
			a.router.View(c, func(r *ui.Route) {
				r.Page().Gap(pageGap)
				switch {
				case r.Match("/restore/tasks"):
					a.restoreTaskList(c)
				case r.Match("/restore/points"):
					a.restoreLocationPoints(c)
				case r.Match("/restore"):
					a.restorePage(c)
				case r.Match("/agents"):
					r.Title(T("nav.agents"))
					a.homePage(c)
				case r.Match("/agents/{server}/{instance}"):
					a.agentPage(c, r.Param("server"), r.Param("instance"))
				case r.Match("/native/{server}/{instance}"):
					a.nativePage(c, r.Param("server"), r.Param("instance"))
				case r.Match("/servers"):
					r.Title(T("nav.servers"))
					a.serversPage(c)
				case r.Match("/servers/add"):
					r.Title(T("servers.add"))
					a.wizardPage(c)
				case r.Match("/servers/{name}/agents"):
					a.onServerPage(c, r.Param("name"))
				case r.Match("/servers/{name}/install/{profile}"):
					a.installPage(c, r.Param("name"), r.Param("profile"))
				case r.Match("/servers/{name}/install-custom"):
					a.customInstallPage(c, r.Param("name"))
				case r.Match("/servers/{name}/rename"):
					a.renamePage(c, r.Param("name"))
				case r.Match("/servers/{name}/migrate"):
					a.migratePage(c, r.Param("name"))
				case r.Match("/servers/{name}/porter"):
					a.porterPage(c, r.Param("name"))
				case r.Match("/servers/{name}"):
					a.serverPage(c, r.Param("name"))
				case r.Match("/settings"):
					r.Title(T("nav.settings"))
					a.settingsPage(c)
				case r.Match("/settings/recover"):
					a.recoverPage(c)
				case r.Match("/settings/admin-rebuild"):
					a.rebuildPage(c)
				case r.Match("/locations"):
					r.Title(T("nav.locations"))
					a.locationsPage(c)
				case r.Match("/locations/add"):
					r.Title(T("loc.add_title"))
					a.locAddPage(c)
				case r.Match("/locations/add/cloud"):
					r.Title(T("loc.cloud_title"))
					a.cloudAddPage(c)
				case r.Match("/locations/{name}"):
					a.locationPage(c, r.Param("name"))
				default:
					ui.Text(c, T("page.not_found"))
				}
			})
		})
	})
}
