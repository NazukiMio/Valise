package gui

import (
	"github.com/egoist/mygo/ui"
)

// View builds the window from the current theme's shell layout (docs/UI.md
// 7.4). Confirmations and the passphrase dialog are outside the layout:
// every theme gets them as the classic theme draws them.
func (a *App) View(c *ui.Context) {
	a.chooseTheme(c.Theme().Dark)
	c.SetTheme(widgets(c.Theme()))
	shell := *cur.Shell
	shell.Grow = max(shell.Grow, 1)
	root := ui.Column(c).Fill().AlignItems(ui.Stretch).Background(palette.Paper)
	if img := cur.Images["backdrop"]; img != nil {
		root.Draw(func(p *ui.Painter, r ui.Rect) { p.Image(img, r, ui.Cover) })
	}
	root.Children(func() {
		render(c, &shell, map[string]func(*ui.Context, *Node){
			"brand": a.brandSlot, "nav": a.navSlot, "tagline": taglineSlot, "theme": a.themeSlot, "page": a.pageSlot,
		})
	})
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
	muted(ui.Text(c, T("shell.tagline"))).FontSize(11).Padding(0, 8)
}

// navItem is one first-level entry.
type navItem struct {
	path  string
	icon  *ui.SVG
	label string
}

func navItems() []navItem {
	return []navItem{{"/agents", icons.agent, T("nav.agents")}, {"/servers", icons.server, T("nav.servers")},
		{"/locations", icons.folder, T("nav.locations")}}
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
		if n.box(ui.Sidebar(c, &page, func() {
			for _, it := range navItems() {
				ui.SidebarItem(c, it.path, it.icon, it.label)
			}
		})).Background(ui.RGBA(0, 0, 0, 0)).TextColor(palette.SideText).Changed() {
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
	} else if b.Hovered() {
		b.Background(palette.Hover)
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
		col := ui.Column(c).Gap(20)
		if n.Padding == nil {
			col.Padding(36, 44)
		}
		inner := *n
		inner.Grow, inner.Background, inner.Image, inner.Radius = 0, "", "", 0
		inner.box(col).Children(func() {
			a.router.View(c, func(r *ui.Route) {
				r.Page().Gap(20)
				switch {
				case r.Match("/agents"):
					r.Title(T("nav.agents"))
					a.homePage(c)
				case r.Match("/agents/{server}/{instance}"):
					a.agentPage(c, r.Param("server"), r.Param("instance"))
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
				case r.Match("/servers/{name}/rename"):
					a.renamePage(c, r.Param("name"))
				case r.Match("/servers/{name}/migrate"):
					a.migratePage(c, r.Param("name"))
				case r.Match("/servers/{name}/porter"):
					a.porterPage(c, r.Param("name"))
				case r.Match("/servers/{name}"):
					a.serverPage(c, r.Param("name"))
				case r.Match("/locations"):
					r.Title(T("nav.locations"))
					a.locationsPage(c)
				default:
					ui.Text(c, T("page.not_found"))
				}
			})
		})
	})
}
