package gui

import (
	"github.com/egoist/mygo/ui"

	"valise/core/protection"
	"valise/core/vault"
)

type oneOffBackup struct {
	tier    string
	copy    bool
	planned bool
	note    string
}

func (a *App) oneOffDialog(c *ui.Context, row *agentRow, ps *protState) {
	e := ps.oneOff
	open := true
	if ui.Modal(c, &open, func() {
		dialog(ui.Column(c), 500).Children(func() {
			title, action := T("backup.once.title"), T("backup.once.go")
			if e.planned {
				title, action = T("backup.now"), T("savepoint.create")
			}
			dialogTitle(ui.Text(c, title))
			if !e.planned {
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
			}
			note, noteOK := "", true
			if ps.savepointsSupported {
				note, noteOK = savepointNoteInput(c, &e.note)
			}
			if row.HasService {
				muted(ui.Text(c, T("backup.once.stop_note"))).Wrap()
			}
			if ps.hasUnknownBackup() {
				ui.Text(c, T("backup.new_warning")).TextColor(palette.Warn).Wrap()
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				ui.Button(c, T("common.cancel")).OnClick(func() { ps.oneOff = nil })
				ui.PrimaryButton(c, action).Disabled(!noteOK).OnClick(func() {
					selection := protection.BackupSelection{Tier: e.tier, SkipCopies: !e.planned && !e.copy, Note: note}
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

func savepointNoteInput(c *ui.Context, value *string) (string, bool) {
	labeled(c, T("savepoint.note"), func() { ui.TextInput(c, value).Label(T("savepoint.note")).Placeholder(T("savepoint.note.example")) })
	note, err := vault.NormalizeNote(*value)
	if err != nil {
		ui.Text(c, T("savepoint.note.invalid")).TextColor(palette.Warn).Wrap()
	} else {
		muted(ui.Text(c, T("savepoint.note.public"))).Wrap()
	}
	return note, err == nil
}
