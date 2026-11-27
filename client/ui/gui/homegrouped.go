package gui

import (
	"github.com/egoist/mygo/ui"
)

// groupedAgentEntry pairs a main agent (SubName == "") with any same-server,
// same-harness sub-agents folded under it in the home page's grouped view (WP6).
type groupedAgentEntry struct {
	main agentRow
	subs []agentRow
}

func foldSubAgents(rows []agentRow) []groupedAgentEntry {
	mainIdx := map[string]int{}
	var entries []groupedAgentEntry
	var orphans []agentRow
	for _, r := range rows {
		if !r.IsSubAgent() {
			k := r.Server + "/" + r.Profile
			mainIdx[k] = len(entries)
			entries = append(entries, groupedAgentEntry{main: r})
		}
	}
	for _, r := range rows {
		if !r.IsSubAgent() {
			continue
		}
		k := r.Server + "/" + r.Profile
		if idx, ok := mainIdx[k]; ok && r.Profile != "" {
			entries[idx].subs = append(entries[idx].subs, r)
		} else {
			orphans = append(orphans, r)
		}
	}
	for _, r := range orphans {
		entries = append(entries, groupedAgentEntry{main: r})
	}
	return entries
}

func subTroubleCount(subs []agentRow) int {
	n := 0
	for _, s := range subs {
		if trouble(s) {
			n++
		}
	}
	return n
}

func (a *App) agentGroupedGrid(c *ui.Context, n *Node, rows []agentRow) {
	look, ok := cardLooks[n.Style]
	if !ok {
		look = cardLooks["normal"]
	}
	if n.Columns > 0 {
		look.columns = n.Columns
	}
	grid := Node{Gap: n.Gap, Padding: n.Padding, Width: n.Width, MaxWidth: n.MaxWidth, Grow: n.Grow, Background: n.Background, Image: n.Image, Radius: n.Radius}
	if grid.Gap == nil {
		grid.Gap = gap(14)
	}
	entries := foldSubAgents(rows)
	grid.box(ui.Grid(c).Columns(look.columns)).Children(func() {
		for _, ent := range entries {
			a.groupedAgentCard(c.Key("grp-"+ent.main.Server+"/"+ent.main.Instance), ent, look)
		}
	})
}

func (a *App) groupedAgentCard(c *ui.Context, ent groupedAgentEntry, look cardLook) {
	r := ent.main
	if len(ent.subs) == 0 {
		a.agentCard(c, r, look)
		return
	}
	bad := trouble(r)
	subBad := subTroubleCount(ent.subs)
	outer := card(ui.Column(c)).Padding(look.pad).Gap(spItem)
	if bad || subBad > 0 {
		outer.Border(1.5, palette.TroubleBorder)
	}
	outer.Children(func() {
		head := pressable(ui.Column(c).Gap(spItem).Padding(spHair).Radius(radPanel), agentLabel(r), radPanel)
		head.OnClick(func() { a.openAgent(r.Server, r.Instance) }).Children(func() {
			ui.Row(c).Gap(spItem).AlignItems(ui.Center).Children(func() {
				a.avatar(c, r, look.avatar)
				ui.Column(c).Grow(1).Gap(3).Children(func() {
					ui.Text(c, r.Name).FontSize(look.name).Bold().MaxLines(1)
					muted(ui.Text(c, r.Harness)).MaxLines(1)
				})
			})
			ui.Column(c).Gap(spHair).Children(func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, icons.server).FontSize(fsSub).TextColor(palette.TextSub)
					muted(ui.Text(c, r.Server)).MaxLines(1)
				})
				a.stateLine(c, r)
			})
		})
		a.subAgentFoldBlock(c, r, ent.subs, subBad)
	})
}

func (a *App) agentGroupedList(c *ui.Context, key string, rows []agentRow) {
	entries := foldSubAgents(rows)
	card(ui.Column(c)).Padding(spHair + 2).Gap(0).Children(func() {
		for i, ent := range entries {
			if i > 0 {
				ui.Divider(c)
			}
			ui.Column(c.Key(key + ent.main.Server + "/" + ent.main.Instance)).Gap(spHair).Children(func() {
				a.agentLine(c, ent.main)
				if len(ent.subs) > 0 {
					ui.Box(c).Padding(0, spItem, spInside, spSection).Children(func() {
						a.subAgentFoldBlock(c, ent.main, ent.subs, subTroubleCount(ent.subs))
					})
				}
			})
		}
	})
}

func (a *App) subAgentFoldBlock(c *ui.Context, main agentRow, subs []agentRow, subBad int) {
	if subBad > 0 {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(7, 7).Radius(4).Background(palette.Warn)
			ui.Text(c, T("home.subagents.trouble", "n", subBad)).FontSize(fsCaption).Bold().TextColor(palette.Warn)
		})
	}
	open := a.fold("subgrp-" + main.Server + "/" + main.Instance)
	ui.Collapsible(c, T("home.subagents.count", "n", len(subs)), open, func() {
		subPanel(ui.Column(c)).Gap(spHair).Children(func() {
			for _, s := range subs {
				sub := s
				row := pressable(ui.Row(c.Key("sub-"+sub.Instance)).Gap(spInside).Padding(spHair, spInside).Radius(radControl).AlignItems(ui.Center), agentLabel(sub), radControl)
				row.OnClick(func() { a.openAgent(sub.Server, sub.Instance) }).Children(func() {
					ui.Text(c, sub.Name).FontSize(fsSub).Bold().Grow(1).MaxLines(1)
					a.stateLine(c, sub)
					chevron(c)
				})
			}
		})
	})
}
