package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"valise/ui/service"
)

// Below 80×24 only a notice shows and keys wait; a confirmation that does
// not fit scrolls, and yes waits until all of it has been on screen.
func TestWindowMinimumAndConfirmScroll(t *testing.T) {
	m, _ := world(t)
	drive(t, m, m.Init())
	m.Update(tea.WindowSizeMsg{Width: 70, Height: 20})
	wantText(t, m, "窗口太小", "80×24")
	key(t, m, "a") // ignored while too small
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if strings.Contains(screenText(m), "窗口太小") {
		t.Fatal("notice at the minimum")
	}
	yes := false
	var detail []string
	for i := range 40 {
		detail = append(detail, "第 "+itoa(i+1)+" 行："+strings.Repeat("很长的命令参数 ", 12))
	}
	drive(t, m, m.push(confirm("执行？", detail, func(m *Model) tea.Cmd { yes = true; return nil })))
	wantText(t, m, "看完才能确认")
	key(t, m, "y")
	if yes {
		t.Fatal("confirmed before the end was shown")
	}
	for range 60 {
		key(t, m, "space")
	}
	wantText(t, m, "第 40 行")
	key(t, m, "y")
	if !yes {
		t.Fatal("not confirmed after reading it all")
	}
}

// The overview's "需要处理" never says 无 over health problems, waits for
// health, and counts what was dismissed (review F07).
func TestOverviewNeeds(t *testing.T) {
	m, _ := world(t)
	drive(t, m, m.Init())
	p := newInstancePage("box", m.server("box").instances[0])
	d := m.server("box")
	if v := needsView(m, p, d); !strings.Contains(v, "正在读取健康状态") {
		t.Fatalf("loading: %s", v)
	}
	p.healthErr = errors.New("health RPC failed")
	if v := needsView(m, p, d); !strings.Contains(v, "健康状态读取失败，暂无法判断") || strings.Contains(v, "需要处理：无") {
		t.Fatalf("failed: %s", v)
	}
	p.healthErr = nil
	bad := service.HealthItem{ID: "approval", Level: service.HealthBad, Title: "审批", Text: "Bypass"}
	if _, err := m.svc.ToggleDismiss("box", p.inst.InstanceID, bad); err != nil {
		t.Fatal(err)
	}
	p.health = &service.HealthReport{Items: []service.HealthItem{bad,
		{ID: "restore_test", Level: service.HealthWarn, Title: "恢复测试", Text: "还没有测试过"}}}
	m.svc.MarkDismissed("box", p.inst.InstanceID, p.health.Items)
	v := ansiRe.ReplaceAllString(needsView(m, p, d), "")
	if strings.Contains(v, "需要处理：无") || !strings.Contains(v, "恢复测试") || !strings.Contains(v, "另有 1 项已忽略") || !strings.Contains(v, "详见 4 健康") {
		t.Fatalf("with problems: %s", v)
	}
	p.health.Items = p.health.Items[:1]
	if v := ansiRe.ReplaceAllString(needsView(m, p, d), ""); !strings.Contains(v, "需要处理：无") || !strings.Contains(v, "另有 1 项已忽略") {
		t.Fatalf("only dismissed: %s", v)
	}
}
