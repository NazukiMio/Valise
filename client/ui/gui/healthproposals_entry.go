package gui

import (
	"fmt"

	"github.com/egoist/mygo/ui"

	"valise/core/agentkit"
)

func webEntryProposalHead(c *ui.Context, prop agentkit.Proposal) {
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		plainTag(c, T("prop.web_entry_tag"))
		muted(ui.Text(c, when(prop.Created, c.Now()))).FontSize(fsSub)
	})
	if prop.WebEntry != nil {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
			ui.Text(c, prop.WebEntry.DisplayName).FontSize(fsSub).Bold().Selectable()
			mono(muted(ui.Text(c, fmt.Sprintf("127.0.0.1:%d%s", prop.WebEntry.Port, prop.WebEntry.Path)))).FontSize(fsSub).Selectable()
		})
	}
}

func (a *App) webEntryProposalBody(c *ui.Context, prop agentkit.Proposal) {
	notice(c, "info", T("prop.web_entry_readonly_note"))
	if prop.WebEntry != nil && prop.WebEntry.Hint != "" {
		subPanel(ui.Column(c)).Gap(4).Children(func() {
			ui.Text(c, T("prop.web_entry_hint_label")).FontSize(fsCaption).Bold()
			mono(ui.Text(c, prop.WebEntry.Hint)).FontSize(fsSub).Wrap().Selectable()
		})
	}
}
