package gui

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"valise/ui/service"
)

// healthItems draws the findings: what needs attention first, each with its
// repair and what came of it right under it; two or more quiet items fold
// into one line (docs/DESIGN.md 2: normal is quiet). It reports whether the
// running repair was drawn next to its finding.
func (a *App) healthItems(c *ui.Context, row *agentRow, st *healthState, items []service.HealthItem) (fixPlaced, sysPlaced bool) {
	var issues, fine []service.HealthItem
	for _, it := range items {
		if quietItem(it) {
			fine = append(fine, it)
		} else {
			issues = append(issues, it)
		}
	}
	if len(fine) < 2 {
		issues, fine = append(issues, fine...), nil
	}
	if len(issues) > 0 {
		card(ui.Column(c)).Padding(spHair, 0).Gap(0).Children(func() {
			fixes := 0
			for i, it := range issues {
				if i > 0 {
					ui.Divider(c)
				}
				if it.ActionID != "" || it.SystemFix == service.FixLinger {
					fixes++
				}
				ic := c.Key(it.ID)
				a.healthLine(ic, row, st, it, fixes == 1)
				if f := st.fix; f != nil && !f.confirm && f.item.ID == it.ID {
					fixPlaced = true
					ui.Column(ic).Padding(0, spGroup+2, spItem+2, 40).Children(func() { a.fixReport(ic, row, st) })
				}
				if f := st.sys; f != nil && !f.confirm && f.item.ID == it.ID {
					sysPlaced = true
					ui.Column(ic).Padding(0, spGroup+2, spItem+2, 40).Children(func() { a.sysFixReport(ic, row, st) })
				}
			}
		})
	}
	if len(fine) > 0 {
		a.fineItems(c, row, fine)
	}
	return fixPlaced, sysPlaced
}

// quietItem is a passing check with nothing to do.
func quietItem(it service.HealthItem) bool {
	return it.Level == service.HealthOK && it.ActionID == "" && it.SystemFix == "" && it.ID != "restore_test"
}

// fineItems is the folded line of passing checks: its count, their names,
// and the full lines when opened.
func (a *App) fineItems(c *ui.Context, row *agentRow, fine []service.HealthItem) {
	open := a.fold("health-fine-" + row.Server + "/" + row.Instance)
	names := make([]string, len(fine))
	for i, it := range fine {
		names[i] = it.Title
	}
	card(ui.Column(c)).Padding(spHair + 2).Gap(spHair).Clip().Children(func() {
		head := ui.Row(c).Padding(10, 14).Radius(radPanel).Gap(14).AlignItems(ui.Center).OnClick(func() { *open = !*open })
		pressable(head, T("health.fine", "n", len(fine)), radPanel).Children(func() {
			toneDot(c, "ok")
			ui.Text(c, T("health.fine", "n", len(fine))).FontSize(fsBody).Bold()
			muted(ui.Text(c, strings.Join(names, T("common.comma")))).FontSize(fsSub).Grow(1).MaxLines(1)
			ui.Icon(c, icons.chevron).FontSize(fsBody).TextColor(palette.TextSub).Rotate(map[bool]float32{true: 90}[*open])
		})
		if !*open {
			return
		}
		for _, it := range fine {
			ic := c.Key("fine-" + it.ID)
			ui.Divider(ic)
			ui.Row(ic).Padding(10, 18, 10, 40).Gap(10).Children(func() {
				ui.Text(ic, it.Title).FontSize(fsSub).Width(160).Wrap()
				muted(ui.Text(ic, it.Text)).FontSize(fsSub).Grow(1).Wrap()
			})
		}
	})
}
