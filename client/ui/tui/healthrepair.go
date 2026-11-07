package tui

import (
	"sort"

	tea "charm.land/bubbletea/v2"
	"valise/ui/service"
)

type repairReady struct {
	page     *instancePage
	tab      *healthTab
	seq      uint64
	actionID string
	list     *service.ActionList
	err      error
}

func (c *healthTab) repair(m *Model, p *instancePage) tea.Cmd {
	rows := c.rows(m, p)
	if c.cursor >= len(rows) || rows[c.cursor].ActionID == "" {
		return nil
	}
	id := rows[c.cursor].ActionID
	c.repairSeq++
	seq := c.repairSeq
	svc, ctx, host, inst := m.svc, m.ctx, p.host, p.inst.InstanceID
	m.flash, m.flashErr = "正在核对管理动作…", false
	return func() tea.Msg {
		list, err := svc.Actions(ctx, host, inst)
		return repairReady{p, c, seq, id, list, err}
	}
}

func (m *Model) readyRepair(v repairReady) tea.Cmd {
	// A lookup must not open a confirmation over an unrelated page or dialog.
	if m.top() != v.page || v.page.tabs[v.page.active] != v.tab || v.seq != v.tab.repairSeq {
		return nil
	}
	if v.err != nil {
		return func() tea.Msg { return failed(v.err) }
	}
	if v.list.Stale {
		m.flash, m.flashErr = "harness 资料版本不同；请先重新接入，完成后回健康页重新发起动作", true
		for i, t := range v.page.tabs {
			if st, ok := t.(*isetTab); ok {
				st.cursor = isReenroll
				return v.page.switchTo(m, i)
			}
		}
		return nil
	}
	if v.list.RepairAction == v.actionID || v.list.ApprovalRepairAction == v.actionID {
		for _, a := range v.list.Actions {
			if a.ID == v.actionID {
				return startAction(m, v.page, a)
			}
		}
	}
	return func() tea.Msg { return flashMsg("修复动作关联已变化，请刷新健康页后再试") }
}

// refreshInstance starts new generations while result pages are on top.
// This invalidates all pre-action responses for the affected parent pages.
func (m *Model) refreshInstance(host, inst string) tea.Cmd {
	cmds := []tea.Cmd{m.loadServer(host)}
	for _, s := range m.stack {
		if p, ok := s.(*instancePage); ok && p.host == host && p.inst.InstanceID == inst {
			cmds = append(cmds, p.loadHealth(m), p.loadCheckup(m))
		}
	}
	return tea.Batch(cmds...)
}

// topHealth is the n most important problems, bad first.
func topHealth(m *Model, p *instancePage, n int) []service.HealthItem {
	var out []service.HealthItem
	for _, it := range (&healthTab{}).rows(m, p) {
		if (it.Level == service.HealthBad || it.Level == service.HealthWarn) && !it.Quiet() {
			out = append(out, it)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return service.Rank(out[i].Level) < service.Rank(out[j].Level) })
	return out[:min(n, len(out))]
}
