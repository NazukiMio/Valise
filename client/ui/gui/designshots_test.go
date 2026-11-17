package gui

import (
	"os"
	"testing"
	"time"

	"valise/ui/service"
)

// TestDesignShots renders the pages the design guide changed (docs/DESIGN.md)
// when VALISE_SHOTS names a folder; it checks nothing else.
func TestDesignShots(t *testing.T) {
	if os.Getenv("VALISE_SHOTS") == "" {
		t.Skip("VALISE_SHOTS not set")
	}
	a, tt := world(t)
	a.health["cloud/alan"] = &healthState{at: time.Now(), report: &service.HealthReport{Items: []service.HealthItem{
		{ID: "service", Level: service.HealthBad, Title: "服务没有运行", Text: "hermes.service 已停止", ActionID: "start", ActionName: "启动服务"},
		{ID: "port", Level: service.HealthWarn, Title: "管理页端口没有监听", Text: "18789 上没有程序在听"},
		{ID: "pods", Level: service.HealthOK, Title: "备份正常", Text: "最近一次在今天 09:00"},
		{ID: "disk", Level: service.HealthOK, Title: "磁盘空间够用", Text: "还剩 41 GiB"},
		{ID: "clock", Level: service.HealthOK, Title: "时钟准确", Text: "和这台电脑相差 1 秒"}}}}
	a.openAgent("cloud", "alan")
	a.agentTab = tabHealth
	tt.Frame()
	tt.Advance(time.Second)
	shot(t, tt, "design-health")
	*a.fold("health-fine-cloud/alan") = true
	tt.Frame()
	tt.Advance(time.Second)
	shot(t, tt, "design-health-open")
	for _, theme := range []string{ThemeDark, ThemeJournal, ThemeNight} {
		a.setTheme(theme)
		tt.Frame()
		tt.Advance(time.Second)
		shot(t, tt, "design-health-"+theme)
		a.router.Push("/agents")
		tt.Advance(time.Second)
		shot(t, tt, "design-home-"+theme)
		a.openAgent("home", "xiaoman")
		a.agentTab = tabJourney
		tt.Frame()
		tt.Advance(time.Second)
		shot(t, tt, "design-journey-"+theme)
		a.openAgent("cloud", "alan")
		a.agentTab = tabHealth
	}
	a.setTheme(ThemeLight)
	for i, host := range []string{"[sudo] password for ubuntu:", "ubuntu@192.0.2.24's password:"} {
		a.asks = append(a.asks, &passPrompt{host: host, kind: askServer, open: true, reply: make(chan passReply, 1)})
		tt.Frame()
		tt.Advance(time.Second)
		shot(t, tt, "design-ask-"+[]string{"sudo", "server"}[i])
		a.asks = nil
	}
	a.asks = append(a.asks, &passPrompt{kind: askOwner, host: "备份", open: true, reply: make(chan passReply, 1)})
	tt.Frame()
	tt.Advance(time.Second)
	shot(t, tt, "design-ask-owner")
}
