package gui

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"valise/core/journey"
	"valise/ui/service"
)

func TestFolderPickersDescribeTheirOwnPurpose(t *testing.T) {
	a, _ := world(t)
	var title, message string
	a.pickFolder = func(t, m string) (string, error) { title, message = t, m; return "", nil }
	a.importChosen()
	if title != "导入主题" || !strings.Contains(message, "theme.json") {
		t.Fatal(title, message)
	}
	a.pickLocFolder(&locAdd{})
	if title != "选择备份位置" || strings.Contains(message, "theme.json") {
		t.Fatal(title, message)
	}
	for _, step := range []string{kfGate, kfImport} {
		kf := &keyFlow{step: step}
		tt := ui.NewTester(func(c *ui.Context) { a.folderField(c, kf) }, 600, 200)
		click(t, tt, T("key.pick_folder"))
		want := "导出恢复套件"
		if step == kfImport {
			want = "导入恢复套件"
		}
		if title != want || strings.Contains(message, "theme.json") {
			t.Fatal(title, message)
		}
	}
}

func TestThemeControlsAreInPersonalization(t *testing.T) {
	a, tt := world(t)
	if tt.HasText("主题 · 跟随系统") {
		t.Fatal("theme control still in sidebar")
	}
	a.router.Push("/settings")
	tt.Frame()
	click(t, tt, "个性化")
	hasAll(t, tt.Texts(), "主题与语言", "主题 · 跟随系统")
	shot(t, tt, "personalization")
}

func TestJourneyLocationsAreOnePoint(t *testing.T) {
	a, tt := world(t)
	p := a.timeline.value.Items[0]
	p.Copies = append(p.Copies, journey.Copy{LocationRef: journey.LocationRef{Kind: "server_vault", ID: "home"}, SourceLocator: "home/xiaoman/period", Observation: journey.Observation{Status: "fresh"}})
	a.timeline.value = service.JourneyPage{Items: []journey.TimelinePoint{p}}
	a.openAgent("home", "xiaoman")
	a.agentTab = tabJourney
	tt.Frame()
	tt.Advance(time.Second)
	hasAll(t, tt.Texts(), "在服务器中 · home", "本机 · usb", "全部 1 个时刻")
	shot(t, tt, "journey-locations")
}

func TestOneOffSelectionCanBeCancelled(t *testing.T) {
	a, _ := world(t)
	ps := &protState{oneOff: &oneOffBackup{tier: "full"}}
	row := &agentRow{Name: "Demo", HasService: true}
	tt := ui.NewTester(func(c *ui.Context) {
		if ps.oneOff != nil {
			a.oneOffDialog(c, row, ps)
		}
	}, 1000, 800)
	hasAll(t, tt.Texts(), "备份一次…", "不修改保护计划", "建立这次备份")
	if ps.oneOff.copy {
		t.Fatal("one-off unexpectedly defaults to copying history")
	}
	shot(t, tt, "backup-once")
	click(t, tt, "取消")
	if ps.oneOff != nil || ps.backup != nil || a.keyFlow != nil {
		t.Fatal("cancel started a backup")
	}
}
