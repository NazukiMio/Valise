package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"valise/core/agentkit"
	"valise/ui/service"
)

func TestHealthRefreshStateAndLateResponses(t *testing.T) {
	m, _ := world(t)
	drive(t, m, m.Init())
	p := newInstancePage("box", m.server("box").instances[0])
	m.stack = append(m.stack, p)
	p.health = &service.HealthReport{}
	tab := &healthTab{}
	text := func() string { return ansiRe.ReplaceAllString(tab.view(m, p, 100), "") }
	fail := errors.New("Authorization: synthetic-secret")
	p.loadCheckup(m)
	stamp := readStamp{p, p.checkRead.seq}
	m.Update(checkLoaded{nil, fail, stamp})
	if v := text(); !strings.Contains(v, "部分检查未完成") || strings.Contains(v, "一切正常") || strings.Contains(v, "synthetic-secret") {
		t.Fatalf("first failure: %s", v)
	}
	p.loadCheckup(m)
	fresh := &agentkit.CheckupResult{Findings: []agentkit.Finding{{ID: agentkit.FindingLongSession}}}
	m.Update(checkLoaded{fresh, nil, readStamp{p, p.checkRead.seq}})
	at := p.checkRead.at
	if at.IsZero() || p.checkRead.err != nil || p.check != fresh {
		t.Fatal("success not adopted")
	}
	p.loadCheckup(m)
	m.Update(checkLoaded{nil, fail, readStamp{p, p.checkRead.seq}})
	if v := text(); !strings.Contains(v, "上次结果") || !strings.Contains(v, "刷新失败") || !strings.Contains(v, "有一个会话太长了") {
		t.Fatalf("stale success: %s", v)
	}
	if !p.checkRead.at.Equal(at) {
		t.Fatal("failure changed success time")
	}
	// Two refreshes finish out of order. Old failures and successes lose.
	p.loadCheckup(m)
	old := readStamp{p, p.checkRead.seq}
	p.loadCheckup(m)
	current := readStamp{p, p.checkRead.seq}
	empty := &agentkit.CheckupResult{}
	m.Update(checkLoaded{empty, nil, current})
	m.Update(checkLoaded{fresh, nil, old})
	m.Update(checkLoaded{nil, fail, old})
	if p.check != empty || p.checkRead.err != nil || p.checkRead.loading {
		t.Fatal("old response overwrote latest success")
	}
	if v := text(); strings.Contains(v, "未完成") || strings.Contains(v, "上次结果") {
		t.Fatalf("failure not cleared: %s", v)
	}
	// The same protection applies to the health report itself.
	p.loadHealth(m)
	old = readStamp{p, p.healthRead.seq}
	p.loadHealth(m)
	current = readStamp{p, p.healthRead.seq}
	report := &service.HealthReport{}
	m.Update(healthLoaded{report, nil, current})
	m.Update(healthLoaded{nil, fail, old})
	if p.health != report || p.healthErr != nil {
		t.Fatal("old health failure applied")
	}
	p.loadHealth(m)
	m.Update(healthLoaded{nil, fail, readStamp{p, p.healthRead.seq}})
	if p.health != report || !strings.Contains(text(), "上次结果") {
		t.Fatal("last health report lost")
	}
	// A parent beneath another page gets its own result, never the new page's.
	other := newInstancePage("other", p.inst)
	m.stack = append(m.stack, other)
	p.loadCheckup(m)
	m.Update(checkLoaded{fresh, nil, readStamp{p, p.checkRead.seq}})
	if p.check != fresh || other.check != nil {
		t.Fatal("response delivered to wrong page")
	}
	p.loadCheckup(m)
	popped := readStamp{p, p.checkRead.seq}
	m.stack = []screen{other}
	m.Update(checkLoaded{empty, nil, popped})
	if p.check != fresh || other.check != nil {
		t.Fatal("popped page response adopted")
	}
}

func TestMergedHealthDismissAndNavigation(t *testing.T) {
	m, _ := world(t)
	drive(t, m, m.Init())
	previous := chatTabOn
	defer func() { chatTabOn = previous }()
	for _, chat := range []bool{false, true} {
		for _, child := range []bool{false, true} {
			chatTabOn = chat
			p := newInstancePage("box", m.server("box").instances[0])
			if child {
				p = newAgentPage("box", p.inst)
			}
			p.target = &service.LoginTarget{}
			p.health = &service.HealthReport{}
			p.check = &agentkit.CheckupResult{Findings: []agentkit.Finding{{ID: agentkit.FindingLongSession}}}
			p.checkRead.at = time.Now()
			tab := &healthTab{}
			rows := tab.rows(m, p)
			if len(rows) != 1 {
				t.Fatal(rows)
			}
			overview := func() string { return ansiRe.ReplaceAllString((&overviewTab{}).view(m, p, 100), "") }
			v := overview()
			if strings.Count(v, "有一个会话太长了") != 1 || strings.Contains(v, "体检有") || strings.Contains(v, "见体检") {
				t.Fatalf("duplicate/old reminder: %s", v)
			}
			if _, err := m.svc.ToggleDismiss(p.host, p.inst.InstanceID, tab.rows(m, p)[0]); err != nil {
				t.Fatal(err)
			}
			v = overview()
			if strings.Contains(v, "有一个会话太长了") || !strings.Contains(v, "另有 1 项已忽略") {
				t.Fatalf("dismissed finding still shown: %s", v)
			}
			healthNo := 0
			for i, tb := range p.tabs {
				if _, ok := tb.(*healthTab); ok {
					healthNo = i + 1
				}
			}
			if !strings.Contains(v, "见 "+itoa(healthNo)+" 健康") || !strings.Contains(tab.view(m, p, 100), "已忽略") {
				t.Fatalf("wrong navigation: %s", v)
			}
			if _, err := m.svc.ToggleDismiss(p.host, p.inst.InstanceID, tab.rows(m, p)[0]); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(overview(), "有一个会话太长了") {
				t.Fatal("undismiss did not restore finding")
			}
		}
	}
}
