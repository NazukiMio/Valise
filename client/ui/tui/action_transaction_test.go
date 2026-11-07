package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"valise/core/action"
	"valise/core/profile"
	"valise/ui/service"
)

func transactionPageFixture() (*Model, profile.Action, *action.Operation) {
	a := profile.Action{ID: "repair", Name: "审批设置", Risk: profile.RiskConfig, Transaction: &profile.ActionTransaction{Root: "state"}}
	j := &action.Operation{ID: "act_fixture", Plan: action.Plan{InstanceID: "demo", Definition: a}, Phase: "step-started"}
	for i := 0; i < 16; i++ {
		id := "step-" + itoa(i)
		j.Plan.Definition.Steps = append(j.Plan.Definition.Steps, profile.ActionStep{ID: id, Argv: profile.Argv{"example", strings.Repeat("argument", 18)}})
		j.Steps = append(j.Steps, action.StepResult{ID: id, Started: true})
	}
	return &Model{svc: &service.Service{}, width: 78, height: 24}, a, j
}
func TestTransactionConfirmationWaitsForItsOwnPlan(t *testing.T) {
	m, a, j := transactionPageFixture()
	p := newActionConfirm("box", "demo", a)
	enter := tea.KeyPressMsg{Code: tea.KeyEnter}
	if cmd, handled := p.update(m, enter); cmd != nil || !handled {
		t.Fatal("unloaded plan allowed execution")
	}
	other := newActionConfirm("box", "demo", a)
	p.update(m, transactionPlanReady{page: other, plan: &j.Plan})
	if p.plan != nil {
		t.Fatal("another confirmation supplied the plan")
	}
	p.update(m, transactionPlanReady{page: p, plan: &j.Plan})
	if p.plan == nil || !p.seal {
		t.Fatal("plan or default backup missing")
	}
	p.update(m, tea.KeyPressMsg{Code: tea.KeyEnd})
	text := p.view(m)
	if lipgloss.Height(text) > m.bodyHeight() || p.scroll == 0 {
		t.Fatal("long plan not scrollable", text)
	}
	p.update(m, tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	if p.seal {
		t.Fatal("backup choice inaccessible after scrolling")
	}
	if cmd, handled := p.update(m, tea.KeyPressMsg{Code: tea.KeyEscape}); cmd != nil || handled {
		t.Fatal("cancel dispatched execution")
	}
}
func TestTransactionRecoveryKeepsChoicesVisibleAndUnknownStepsBlocked(t *testing.T) {
	m, _, j := transactionPageFixture()
	p := &actionOperation{host: "box", op: j.ID, data: j}
	if len(p.choices()) != 1 {
		t.Fatal("unknown steps offered continue")
	}
	p.update(m, tea.KeyPressMsg{Code: tea.KeyEnd})
	text := p.view(m)
	if !strings.Contains(text, "放弃尚未发布的修改并恢复原服务状态") || lipgloss.Height(text) > m.bodyHeight() {
		t.Fatal(text)
	}
	for i := range j.Steps {
		code := 0
		j.Steps[i].Completed = true
		j.Steps[i].Exit = &code
	}
	j.StageVerified = true
	j.StageTreeDigest = "digest"
	if len(p.choices()) != 2 {
		t.Fatal("verified stage cannot continue")
	}
	p.selected = 1
	j.StageVerified = false
	p.update(m, operationReady{page: p, data: j})
	if p.selected != 0 {
		t.Fatal("removed continue choice stayed selected")
	}
	j.Published = true
	if len(p.choices()) != 1 || !strings.Contains(p.choices()[0], "保留已发布配置") {
		t.Fatal(p.choices())
	}
}
func TestTransactionResultScrollsAllStepResults(t *testing.T) {
	m, a, j := transactionPageFixture()
	r := &actionResult{a: a, res: &service.ActionResult{RunResult: action.RunResult{Operation: j}}}
	r.update(m, tea.KeyPressMsg{Code: tea.KeyDown})
	r.scroll = 1 << 20
	text := r.view(m)
	if r.scroll == 0 || lipgloss.Height(text) > m.bodyHeight() || !strings.Contains(text, "r 查看操作详情与恢复") {
		t.Fatal(text)
	}
}
