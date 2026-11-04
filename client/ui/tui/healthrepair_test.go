package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"valise/core/agentkit"
	"valise/ui/service"
)

func TestHealthRepairFlow(t *testing.T) {
	m, home := world(t)
	prof := demoProfile + `service:
  candidates: [{scope: user, unit: demo.service}]
  repair_action: restart
`
	prof = strings.Replace(prof, "argv: [/bin/echo, ok]", "argv: [\"${HOME}/.local/bin/repair-demo\"]", 1)
	file := filepath.Join(m.svc.Dirs.Data, "profiles/demo.yaml")
	os.WriteFile(file, []byte(prof), 0644)
	os.WriteFile(filepath.Join(home, "active"), []byte("inactive"), 0600)
	os.WriteFile(filepath.Join(home, ".local/bin/systemctl"), []byte("#!/bin/sh\nprintf 'LoadState=loaded\\nActiveState='\ncat \"$HOME/active\"\nprintf '\\n'\n"), 0755)
	os.WriteFile(filepath.Join(home, ".local/bin/repair-demo"), []byte("#!/bin/sh\nprintf active > \"$HOME/active\"\nprintf yes > \"$HOME/executed\"\n"), 0755)
	if _, err := m.svc.Enroll(m.ctx, "box", service.EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatal(err)
	}
	openInstance(t, m)
	key(t, m, "4")
	p := m.top().(*instancePage)
	tab := p.tabs[p.active].(*healthTab)
	selectService := func() {
		for i, it := range tab.rows(m, p) {
			if it.ID == "service" {
				tab.cursor = i
			}
		}
	}
	selectService()
	wantText(t, m, "重启演示", "修复")
	key(t, m, "i") // ignored red problem must remain actionable
	key(t, m, "f")
	if _, ok := m.top().(*actionConfirm); !ok {
		t.Fatalf("not confirmation: %T", m.top())
	}
	wantText(t, m, "会发生什么", "实际命令")
	key(t, m, "esc")
	if _, err := os.Stat(filepath.Join(home, "executed")); !os.IsNotExist(err) {
		t.Fatal("cancel executed action")
	}
	key(t, m, "f")
	oldHealth := readStamp{p, p.healthRead.seq}
	oldServer := *m.server("box")
	oldCheck := readStamp{p, p.checkRead.seq}
	key(t, m, "enter")
	r, ok := m.top().(*actionResult)
	if !ok || r.res == nil || r.res.Verification.State != "resolved" {
		t.Fatalf("result %+v", m.top())
	}
	wantText(t, m, "退出码 0", "该问题复核结果", "本次检查通过")
	if p.healthRead.seq <= oldHealth.seq || p.checkRead.seq <= oldCheck.seq {
		t.Fatal("parent was not refreshed")
	}
	if p.healthRead.loading || p.checkRead.loading {
		t.Fatal("result not delivered to parent beneath result page")
	}
	currentServer := m.server("box")
	m.Update(srvLoaded{&oldServer})
	if m.server("box") != currentServer || currentServer.seq <= oldServer.seq {
		t.Fatal("pre-repair server response overwrote new state")
	}
	current := p.health
	m.Update(healthLoaded{&service.HealthReport{Items: []service.HealthItem{{ID: "service", Level: service.HealthBad}}}, nil, oldHealth})
	m.Update(checkLoaded{&agentkit.CheckupResult{Findings: []agentkit.Finding{{ID: agentkit.FindingLongSession}}}, nil, oldCheck})
	if p.health != current {
		t.Fatal("pre-repair health overwrote new result")
	}
	key(t, m, "esc")
	key(t, m, "1")
	v := screenText(m)
	if strings.Contains(v, "demo.service 没有运行") {
		t.Fatal("parent kept old service failure")
	}
	key(t, m, "4")
	selectService()
	if c := tab.repair(m, p); c != nil {
		t.Fatal("healthy service offers repair")
	}
	// An outdated profile directs users to the existing enrollment entry.
	os.WriteFile(file, []byte(strings.Replace(prof, "profile_version: 1", "profile_version: 2", 1)), 0644)
	os.WriteFile(filepath.Join(home, "active"), []byte("inactive"), 0600)
	drive(t, m, tab.load(m, p))
	selectService()
	key(t, m, "f")
	if _, ok := m.top().(*instancePage); !ok {
		t.Fatal("stale opened action")
	}
	if st, ok := p.tabs[p.active].(*isetTab); !ok || st.cursor != isReenroll {
		t.Fatal("stale did not guide reenrollment")
	}
	wantText(t, m, "资料版本不同", "重新接入")
}

func TestRepairResultKeepsProblem(t *testing.T) {
	m, _ := world(t)
	a := service.ActionResult{Verification: &service.HealthVerification{ItemID: "service", State: "remaining", Text: "问题仍在"}}
	r := &actionResult{res: &a}
	if v := r.view(m); !strings.Contains(v, "退出码 0") || !strings.Contains(v, "问题仍在") {
		t.Fatal(v)
	}
	a.Verification.State, a.Verification.Text = "unknown", "无法复核"
	if v := r.view(m); !strings.Contains(v, "无法复核") {
		t.Fatal(v)
	}
}
