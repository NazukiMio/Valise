package gui

import (
	"sort"

	"github.com/egoist/mygo/ui"
)

// sameHarnessSubAgents returns all active sub-agents (SubName != "") on the
// same server sharing main's harness profile ID.
func sameHarnessSubAgents(rows []agentRow, main agentRow) []agentRow {
	if main.Profile == "" {
		return nil
	}
	var out []agentRow
	for _, r := range rows {
		if !r.Retired() && r.Server == main.Server && r.Profile == main.Profile && r.IsSubAgent() {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Instance < out[j].Instance })
	return out
}

// parentMainAgent returns the active main agent (SubName == "") on the same
// server sharing sub's harness profile ID, if any.
func parentMainAgent(rows []agentRow, sub agentRow) *agentRow {
	if !sub.IsSubAgent() || sub.Profile == "" {
		return nil
	}
	for _, r := range rows {
		if !r.Retired() && r.Server == sub.Server && r.Profile == sub.Profile && !r.IsSubAgent() {
			rc := r
			return &rc
		}
	}
	return nil
}

// subAgentParentBar renders a return link to the parent main agent on a
// sub-agent's detail page (WP6).
func (a *App) subAgentParentBar(c *ui.Context, row *agentRow) {
	if !row.IsSubAgent() {
		return
	}
	parent := parentMainAgent(a.agentRows(a.agents.value), *row)
	if parent == nil {
		return
	}
	subPanel(ui.Row(c)).Gap(spItem).AlignItems(ui.Center).Children(func() {
		muted(ui.Text(c, T("subagents.parent_hint", "name", parent.Name))).FontSize(fsSub).Grow(1).Wrap()
		textLink(c, T("subagents.parent_link", "name", parent.Name), func() {
			a.openAgent(parent.Server, parent.Instance)
		})
	})
}

// subAgentsTab renders the main agent's "子 Agent" tab listing all same-server,
// same-harness sub-agents with honest status, one-click jump, and a shortcut
// to start a same-harness bundle savepoint (WP6).
func (a *App) subAgentsTab(c *ui.Context, row *agentRow) {
	subs := sameHarnessSubAgents(a.agentRows(a.agents.value), *row)
	card(ui.Column(c)).Gap(spItem).Children(func() {
		ui.Row(c).Gap(spInside).AlignItems(ui.Center).Wrap().Children(func() {
			sectionTitle(ui.Text(c, T("subagents.title", "n", len(subs)))).Grow(1)
			if len(subs) > 0 && !row.Retired() {
				ui.Button(c, T("subagents.bundle_backup")).OnClick(func() {
					ps := a.protOf(row)
					ms := ps.manualState()
					ms.members = map[string]bool{}
					for _, s := range subs {
						ms.members[s.Instance] = true
					}
					a.agentTab = tabManage
				})
			}
		})
		muted(ui.Text(c, T("subagents.hint"))).FontSize(fsSub).Wrap()
		if len(subs) == 0 {
			muted(ui.Text(c, T("subagents.empty"))).FontSize(fsBody)
			return
		}
		for _, s := range subs {
			sub := s
			line := pressable(ui.Row(c.Key("sa-"+sub.Instance)).Gap(spItem).Padding(spInside, spItem).Radius(radControl).AlignItems(ui.Center), agentLabel(sub), radControl)
			line.OnClick(func() { a.openAgent(sub.Server, sub.Instance) }).Children(func() {
				a.avatar(c, sub, 32)
				ui.Column(c).Grow(1).Gap(2).Children(func() {
					ui.Text(c, sub.Name).FontSize(fsBody).Bold()
					a.stateLine(c, sub)
				})
				chevron(c)
			})
		}
	})
}
