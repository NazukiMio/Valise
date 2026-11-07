package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"valise/core/action"
	"valise/ui/service"
)

type transactionPlanReady struct {
	page *actionConfirm
	plan *action.Plan
	err  error
}

func (c *actionConfirm) loadTransactionPlan(m *Model) tea.Cmd {
	if c.a.Transaction == nil {
		return nil
	}
	return func() tea.Msg {
		p, err := m.svc.PlanAction(m.ctx, c.host, c.inst, c.a.ID)
		return transactionPlanReady{page: c, plan: p, err: err}
	}
}
func (m *Model) refreshActionAffected(host string, j *action.Operation) tea.Cmd {
	cmds := []tea.Cmd{m.loadServer(host)}
	affected := map[string]bool{j.Plan.InstanceID: true}
	for _, id := range j.Plan.Affected {
		affected[id] = true
	}
	for _, page := range m.stack {
		if p, ok := page.(*instancePage); ok && p.host == host && affected[p.inst.InstanceID] {
			cmds = append(cmds, p.loadHealth(m), p.loadCheckup(m))
		}
		if p, ok := page.(*actionResult); ok && p.host == host && p.res != nil && p.res.Operation != nil && p.res.Operation.ID == j.ID {
			p.res.Operation = j
		}
	}
	return tea.Batch(cmds...)
}

type actionOperation struct {
	host, op         string
	data             *action.Operation
	err              error
	loading, running bool
	selected         int
	scroll           int
}
type operationReady struct {
	page *actionOperation
	data *action.Operation
	err  error
	ran  bool
}

func newActionOperation(host, op string) *actionOperation {
	return &actionOperation{host: host, op: op, loading: true}
}
func (p *actionOperation) title() string    { return "配置操作与恢复" }
func (p *actionOperation) hostName() string { return p.host }
func (p *actionOperation) help() string {
	if p.data != nil && !p.data.Done {
		return menu("↑↓", "选择", "enter", "确认恢复")
	}
	return menu("u", "重新读取")
}
func (p *actionOperation) busy() string {
	if p.running {
		return "正在恢复原服务状态，等待结果"
	}
	return ""
}
func (p *actionOperation) init(m *Model) tea.Cmd {
	p.loading = true
	return func() tea.Msg {
		j, err := m.svc.ActionStatus(m.ctx, p.host, p.op)
		return operationReady{page: p, data: j, err: err}
	}
}
func (p *actionOperation) choices() []string {
	if p.data == nil || p.data.Done {
		return nil
	}
	first := "放弃尚未发布的修改并恢复原服务状态"
	if p.data.Published {
		first = "保留已发布配置并恢复原服务状态"
	}
	result := []string{first}
	if service.CanContinueAction(p.data) {
		result = append(result, "发布已复核的暂存配置并恢复原服务状态")
	}
	return result
}
func (p *actionOperation) update(m *Model, msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case operationReady:
		if msg.page != p {
			return nil, true
		}
		p.loading, p.running = false, false
		p.err = msg.err
		if msg.data != nil {
			p.data = msg.data
		}
		p.selected = min(p.selected, max(0, len(p.choices())-1))
		if msg.ran && p.data != nil {
			return m.refreshActionAffected(p.host, p.data), true
		}
		return nil, true
	case tea.KeyPressMsg:
		if p.running {
			return nil, true
		}
		switch msg.String() {
		case "pgup", "pgdown", "home", "end":
			scrollAction(msg.String(), &p.scroll, m.bodyHeight())
			return nil, true
		case "u":
			return p.init(m), true
		case "up", "k":
			p.selected = max(0, p.selected-1)
			return nil, true
		case "down", "j":
			p.selected = min(max(0, len(p.choices())-1), p.selected+1)
			return nil, true
		case "enter":
			if p.loading || p.err != nil || len(p.choices()) == 0 {
				return nil, true
			}
			choice := action.ResumeRestore
			if p.selected == 1 {
				choice = action.ResumeContinue
			}
			return gateThen(m, p.host, p.data.Plan.InstanceID, func(m *Model) tea.Cmd {
				ctx := m.ctx
				if m.riskOnce {
					ctx = service.WithRiskAccepted(ctx)
				}
				m.riskOnce = false
				p.running = true
				return func() tea.Msg {
					res, err := m.svc.ResumeAction(ctx, p.host, p.op, choice)
					var j *action.Operation
					if res != nil {
						j = res.Operation
					}
					return operationReady{page: p, data: j, err: err, ran: true}
				}
			}), true
		}
	}
	return nil, false
}
func (p *actionOperation) view(m *Model) string {
	if p.loading {
		return dimStyle.Render("正在读取配置操作记录…")
	}
	rows := []string{headStyle.Render("配置操作与恢复"), ""}
	var footer []string
	if p.data != nil {
		rows = append(rows, service.ActionOperationLines(p.data)...)
		rows = append(rows, "", dimStyle.Render("操作："+p.op))
		if !p.data.Done {
			footer = append(footer, "", warnStyle.Render("配置执行结果不明的步骤不会重跑；退出此页不会修改服务器。"))
			for i, label := range p.choices() {
				prefix := "  "
				if i == p.selected {
					prefix = "› "
				}
				footer = append(footer, prefix+label)
			}
		}
	}
	if p.err != nil {
		rows = append(rows, "", errStyle.Render(errText(p.err)))
	}
	if p.running {
		rows = append(rows, "", dimStyle.Render("正在恢复并复核…"))
	}
	if len(footer) == 0 {
		return actionViewport(m, strings.Join(rows, "\n"), &p.scroll, m.bodyHeight())
	}
	foot := lipgloss.NewStyle().Width(max(1, m.width-2)).Render(strings.Join(footer, "\n"))
	return actionViewport(m, strings.Join(rows, "\n"), &p.scroll, m.bodyHeight()-lipgloss.Height(foot)-1) + "\n" + foot
}
