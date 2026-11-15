package gui

import (
	"github.com/egoist/mygo/ui"
	"valise/core/protection"
)

type oneOffBackup struct {
	tier string
	copy bool
}

func (a *App) oneOffDialog(c *ui.Context, row *agentRow, ps *protState) {
	e := ps.oneOff
	open := true
	if ui.Modal(c, &open, func() {
		ui.Column(c).Width(500).Padding(24).Gap(12).Children(func() {
			ui.Text(c, T("backup.once.title")).FontSize(18).Bold()
			muted(ui.Text(c, T("backup.once.note"))).Wrap()
			ui.RadioGroup(c, func() {
				for _, tier := range []string{"soul", "soul-secrets", "full", "raw"} {
					ui.Radio(c.Key(tier), &e.tier, tier, tierText(tier))
				}
			}).Label(T("backup.once.scope"))
			muted(ui.Text(c, oneOffScopeText(e.tier))).Wrap()
			ui.Checkbox(c, &e.copy, T("backup.once.copy"))
			if e.copy {
				muted(ui.Text(c, T("backup.once.copy_note"))).Wrap()
			}
			if row.HasService {
				muted(ui.Text(c, T("backup.once.stop_note"))).Wrap()
			}
			if ps.hasUnknownBackup() {
				ui.Text(c, T("backup.new_warning")).TextColor(palette.Warn).Wrap()
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				ui.Button(c, T("common.cancel")).OnClick(func() { ps.oneOff = nil })
				ui.PrimaryButton(c, T("backup.once.go")).OnClick(func() {
					selection := protection.BackupSelection{Tier: e.tier, SkipCopies: !e.copy}
					ps.oneOff = nil
					a.needKey(func() { a.runSelectedBackup(row, ps, selection) })
				})
			})
		})
	}).Dismissed() {
		ps.oneOff = nil
	}
}

func oneOffScopeText(tier string) string {
	switch tier {
	case "soul":
		return T("backup.once.scope.soul")
	case "soul-secrets":
		return T("backup.once.scope.secrets")
	case "raw":
		return T("backup.once.scope.raw")
	}
	return T("backup.once.scope.full")
}
