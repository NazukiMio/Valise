package gui

import (
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"

	"valise/core/protection"
)

// sameHarnessPeers returns other non-retired agents on the same server sharing
// the same harness profile ID.
func sameHarnessPeers(rows []agentRow, self agentRow) []agentRow {
	if self.Profile == "" {
		return nil
	}
	var out []agentRow
	for _, r := range rows {
		if !r.Retired() && r.Server == self.Server && r.Profile == self.Profile && r.Instance != self.Instance {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsSubAgent() != out[j].IsSubAgent() {
			return !out[i].IsSubAgent()
		}
		return out[i].Instance < out[j].Instance
	})
	return out
}

func fullCycleText(cycle string) string {
	switch cycle {
	case protection.FullCycleWeekly:
		return T("protect.fullcycle.weekly")
	case protection.FullCycleMonthly:
		return T("protect.fullcycle.monthly")
	default:
		return T("protect.fullcycle.none")
	}
}

func bundleMembersText(self string, members []string) string {
	var others []string
	for _, m := range members {
		if m != self {
			others = append(others, m)
		}
	}
	if len(others) == 0 {
		return T("protect.bundle.none")
	}
	return T("protect.bundle.with", "members", strings.Join(others, T("common.list_sep")))
}

func (a *App) scheduleFullCycleFields(c *ui.Context, e *schedEdit) {
	ui.Box(c).Height(1).Background(palette.Line)
	ui.Text(c, T("protect.fullcycle.title")).Bold()
	muted(ui.Text(c, T("protect.fullcycle.hint"))).FontSize(fsCaption).Wrap()
	ui.RadioGroup(c, func() {
		for _, fc := range []string{protection.FullCycleNone, protection.FullCycleWeekly, protection.FullCycleMonthly} {
			ui.Radio(c.Key("fc-"+fc), &e.fullCycle, fc, fullCycleText(fc))
		}
	}).Row().Gap(spItem).Wrap().Label(T("protect.fullcycle.title"))
}

func (a *App) scheduleBundleFields(c *ui.Context, row *agentRow, e *schedEdit) {
	peers := sameHarnessPeers(a.agentRows(a.agents.value), *row)
	if len(peers) == 0 {
		return
	}
	ui.Box(c).Height(1).Background(palette.Line)
	ui.Text(c, T("protect.bundle.title")).Bold()
	muted(ui.Text(c, T("protect.bundle.hint"))).FontSize(fsCaption).Wrap()
	if e.bundleMembers == nil {
		e.bundleMembers = map[string]bool{}
	}
	for _, p := range peers {
		checked := e.bundleMembers[p.Instance]
		if ui.Checkbox(c.Key("pb-"+p.Instance), &checked, p.Name).Changed() {
			e.bundleMembers[p.Instance] = checked
		}
	}
}

func buildBundleMembersFromEdit(self string, checked map[string]bool) []string {
	var out []string
	for id, on := range checked {
		if on && id != self {
			out = append(out, id)
		}
	}
	if len(out) == 0 {
		return nil
	}
	out = append(out, self)
	sort.Strings(out)
	return out
}
