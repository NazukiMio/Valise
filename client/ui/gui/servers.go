package gui

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

// serversPage lists the servers this computer knows, each with its state
// in one of four words (docs/UI.md 7.5), and adds new ones.
func (a *App) serversPage(c *ui.Context) {
	if !a.servers.started {
		a.refreshServers()
	}
	ui.Row(c).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Children(func() { pageHead(c, T("servers.title"), T("servers.sub")) })
		ui.PrimaryButton(c, T("servers.add")).OnClick(func() { a.startWizard(false) })
	})
	if a.servers.err != nil {
		errorBox(c, a.servers.err, a.fold("servers-err"))
	}
	rows := a.servers.value
	if len(rows) == 0 {
		if a.servers.busy {
			ui.Spinner(c)
		} else if a.servers.err == nil {
			muted(ui.Text(c, T("servers.empty"))).FontSize(14)
		}
		return
	}
	card(ui.Column(c)).Padding(4, 0).Gap(0).Children(func() {
		for i, r := range rows {
			if i > 0 {
				ui.Divider(c)
			}
			a.serverLine(c.Key(r.Name), r)
		}
	})
}

// agentsOn counts the agents in use on a server.
func (a *App) agentsOn(name string) int {
	n := 0
	for _, r := range a.agentRows(a.agents.value) {
		if r.Server == name && !r.Retired() {
			n++
		}
	}
	return n
}

func (a *App) serverLine(c *ui.Context, r serverRow) {
	st := a.serverState(r.Name, c.Now())
	line := ui.Row(c).Padding(13, 18).Gap(14).AlignItems(ui.Center).Cursor(ui.CursorPointer).Role(ui.RoleButton).
		Label(r.Name + "，" + st.Word)
	if line.Hovered() {
		line.Background(palette.Hover)
	}
	line.OnClick(func() { a.router.Push("/servers/" + r.Name) }).Children(func() {
		ui.Box(c).Size(8, 8).Radius(4).Background(toneColor(st.Tone))
		ui.Column(c).Grow(1).Gap(3).Children(func() {
			ui.Text(c, r.Name).Bold()
			parts := []string{r.Kind}
			if r.Address != "" {
				parts = append(parts, r.Address)
			}
			if n := a.agentsOn(r.Name); n > 0 {
				parts = append(parts, T("servers.agents", "n", n))
			} else if st.Key == "online" {
				parts = append(parts, T("servers.no_agents"))
			}
			if r.Valise && a.svc.HasAdminKey(r.Name) {
				parts = append(parts, T("servers.has_admin"))
			}
			if st.Key != "online" && st.Reason != "" {
				parts = append(parts, st.Reason)
			}
			if st.Old != "" {
				parts = append(parts, st.Old)
			}
			muted(ui.Text(c, strings.Join(parts, T("common.dot_sep")))).MaxLines(2)
		})
		stateTag(c, st)
		ui.Text(c, "›").FontSize(18).TextColor(palette.Line)
	})
}

// stateTag is a server's word as a small tag; only 需要处理 is colored.
func stateTag(c *ui.Context, st serverState) {
	t := ui.Box(c).Padding(2, 10).Radius(999).Border(1, palette.Line)
	ink := palette.TextSub
	switch st.Tone {
	case "ok":
		ink = palette.Ok
		t.Border(1, palette.Ok.Alpha(0.5))
	case "warn":
		ink = palette.Warn
		t.Border(1, palette.Warn.Alpha(0.6)).Background(palette.AlertBg)
	}
	t.Children(func() { ui.Text(c, st.Word).FontSize(12).TextColor(ink) })
}
