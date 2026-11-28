package gui

import (
	"github.com/egoist/mygo/ui"
)

func (a *App) isRetiredAgent(server, instance string) bool {
	for _, r := range a.agentRows(a.agents.value) {
		if r.Server == server && r.Instance == instance {
			return r.Retired()
		}
	}
	return false
}

func (a *App) refreshProposalsOnVisit(server, instance string) {
	if server == "" || instance == "" || a.isRetiredAgent(server, instance) {
		return
	}
	a.readProposals(server, instance)
}

func (a *App) proposalsPendingBanner(c *ui.Context, row *agentRow) {
	if row == nil || row.Retired() {
		return
	}
	st := a.proposalsOf(row.Server, row.Instance)
	if a.agentTab == tabHealth || len(st.items) == 0 {
		return
	}
	reportPanel(ui.Row(c)).Gap(10).AlignItems(ui.Center).Wrap().Children(func() {
		toneDot(c, "warn")
		ui.Text(c, T("prop.pending_banner", "n", len(st.items))).FontSize(fsSub).Bold().Grow(1).Wrap()
		ui.Button(c, T("prop.pending_review")).OnClick(func() {
			a.agentTab = tabHealth
			a.readProposals(row.Server, row.Instance)
		})
	})
}
