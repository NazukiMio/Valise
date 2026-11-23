package gui

import (
	"github.com/egoist/mygo/ui"

	"valise/core/protection"
	"valise/core/vault"
)

type tierEdit struct {
	tier string
}

func newTierEdit(d protection.Desired) *tierEdit {
	return &tierEdit{tier: orText(d.Scope.Tier, vault.TierFull)}
}

var protectTierChoices = []struct {
	id    string
	label string
	desc  string
}{
	{vault.TierFull, "protect.tier.full.label", "protect.tier.full.desc"},
	{vault.TierSoulSecrets, "protect.tier.soul_secrets.label", "protect.tier.soul_secrets.desc"},
	{vault.TierSoul, "protect.tier.soul.label", "protect.tier.soul.desc"},
}

func (a *App) tierDialog(c *ui.Context, row *agentRow, ps *protState) {
	e := ps.tier
	if e == nil {
		return
	}
	open := true
	if ui.Modal(c, &open, func() {
		dialog(ui.Column(c), 500).Children(func() {
			dialogTitle(ui.Text(c, T("protect.tier.title")))
			muted(ui.Text(c, T("protect.tier.note"))).Wrap()
			ui.RadioGroup(c, func() {
				for _, ch := range protectTierChoices {
					ui.Radio(c.Key("tier-"+ch.id), &e.tier, ch.id, T(ch.label))
					muted(ui.Text(c, T(ch.desc))).FontSize(fsSub).Wrap()
				}
			}).Label(T("protect.tier.title"))
			if e.tier == vault.TierSoul {
				notice(c, "warn", T("protect.tier.soul_warn"))
			}
			ui.Row(c).Gap(8).AlignItems(ui.Center).Justify(ui.End).Children(func() {
				needTag(c, T("need.valise"))
				ui.Button(c, T("common.cancel")).OnClick(func() { ps.tier = nil })
				ui.PrimaryButton(c, T("protect.save")).OnClick(func() {
					next := ps.desired()
					next.Scope.Kind = "preset"
					next.Scope.Tier = orText(e.tier, vault.TierFull)
					ps.tier = nil
					a.applyDesired(row, ps, next)
				})
			})
		})
	}).Dismissed() {
		ps.tier = nil
	}
}
