package gui

import (
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/client/config"
	"valise/core/enroll"
	"valise/core/identity"
	"valise/core/journey"
	"valise/core/valise"
	"valise/ui/service"
)

// world is an App over fixed data: two agents on two servers, one server
// unread, and three saved points of one agent in a local repository.
func world(t *testing.T) (*App, *ui.Tester) {
	t.Helper()
	svc := &service.Service{Dirs: config.Dirs{Data: t.TempDir(), Cache: t.TempDir()}}
	a := New(svc)
	a.run = func(f func()) { f() }
	a.harness["openclaw"] = "OpenClaw"
	at := time.Date(2026, 11, 8, 9, 0, 0, 0, time.Local)
	a.agents = load[service.AgentListResult]{started: true, value: service.AgentListResult{
		Items: []service.AgentSummary{
			{ConnectionID: "home", Instance: enroll.InstanceSummary{InstanceID: "xiaoman", Label: "小满", ProfileID: "openclaw",
				Service: []enroll.ServiceState{{Unit: "openclaw-gateway.service", Active: "active"}},
				Entries: []enroll.EntryState{{ID: "gateway", Port: 18789, Listening: false}},
				Roots:   map[string]string{"state": "/home/me/.openclaw"}}},
			{ConnectionID: "cloud", Instance: enroll.InstanceSummary{InstanceID: "alan", Label: "阿蓝", ProfileID: "hermes",
				Service: []enroll.ServiceState{{Unit: "hermes.service", Active: "inactive"}}}},
		},
		Observations: []service.AgentServerObservation{
			{ConnectionID: "home", Status: "fresh"}, {ConnectionID: "cloud", Status: "fresh"},
			{ConnectionID: "nas", Status: "unknown", Error: &valise.UserError{Code: "ssh_connect_failed", Reason: "连不上"}},
		},
		Partial: true,
	}}
	var items []journey.TimelinePoint
	for i, tier := range []string{"full", "soul", "soul-secrets"} {
		items = append(items, journey.TimelinePoint{
			SnapshotRef: journey.SnapshotRef{VaultID: "v1", SnapshotID: "s" + string(rune('a'+i))},
			CreatedAt:   at.Add(-time.Duration(i) * 26 * time.Hour), RecordedInstanceID: "xiaoman",
			Scope: journey.Scope{Tier: tier}, FileCount: 10 + i, PlainBytes: 2048,
			Copies: []journey.Copy{{LocationRef: journey.LocationRef{Kind: "local_repo", ID: "usb"}, SourceLocator: "home/xiaoman/2026-11-01_2026-11-30"}},
		})
	}
	// The same instance id on another server is another agent.
	items = append(items, journey.TimelinePoint{SnapshotRef: journey.SnapshotRef{SnapshotID: "other"}, CreatedAt: at, RecordedInstanceID: "xiaoman",
		Copies: []journey.Copy{{SourceLocator: "cloud/xiaoman/2026-11-01_2026-11-30"}}})
	a.timeline = load[service.JourneyPage]{started: true, value: service.JourneyPage{Items: items}}
	return a, ui.NewTester(a.View, 1180, 900)
}

// click clicks and lets page transitions finish.
func click(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second)
}

func TestHomeKeepsStatesApart(t *testing.T) {
	_, tt := world(t)
	// Each fact sits in its own cell of the status board.
	for _, want := range []string{"小满", "阿蓝", "OpenClaw · home", "服务", "运行中", "管理页端口", "没有监听", "已停止", "nas 这次没有读到：连不上", "本机还没有", "本机副本", "usb"} {
		if !tt.HasText(want) {
			t.Errorf("missing %q in %q", want, tt.Texts())
		}
	}
	// The sidebar shows only what exists.
	for _, absent := range []string{"工具箱", "备份位置"} {
		if tt.HasText(absent) {
			t.Errorf("unimplemented entry %q drawn", absent)
		}
	}
}

func TestAgentOverviewAndJourney(t *testing.T) {
	a, tt := world(t)
	click(t, tt, "小满")
	if a.router.Path() != "/agents/home/xiaoman" || !tt.HasText("正在 home 上") || !tt.HasText("完整备份") {
		t.Fatalf("overview: %s %q", a.router.Path(), tt.Texts())
	}
	click(t, tt, "查看完整旅程")
	if !tt.HasText("小满 走过的路") || !tt.HasText("2026 年 11 月") || !tt.HasText("轻装备份（人格与记忆）") {
		t.Fatalf("journey: %q", tt.Texts())
	}
	// Another server's point with the same instance id is not listed.
	if n := len(pointsFor(a.timeline.value, "home", "xiaoman")); n != 3 {
		t.Fatalf("points %d", n)
	}
	click(t, tt, when(a.timeline.value.Items[1].CreatedAt, tt.Now()))
	if a.point != "sb" || !tt.HasText("这次带上了什么") || !tt.HasText("仓库 usb") {
		t.Fatalf("detail %q: %q", a.point, tt.Texts())
	}
	// No restore button until the restore segment.
	if tt.HasText("恢复") {
		t.Fatalf("restore offered: %q", tt.Texts())
	}
	if err := tt.Click("更早"); err != nil || a.point != "sc" {
		t.Fatalf("earlier: %v %s", err, a.point)
	}
	if err := tt.Click("‹ 返回旅程"); err != nil || a.point != "" {
		t.Fatalf("back: %v %s", err, a.point)
	}
}

func TestServersPageAndErrors(t *testing.T) {
	a, tt := world(t)
	click(t, tt, "服务器")
	if a.router.Path() != "/servers" || !tt.HasText("还没有添加服务器。") {
		t.Fatalf("%s %q", a.router.Path(), tt.Texts())
	}
	// A UserError shows reason and suggestion; details stay folded.
	a.servers.err = &valise.UserError{Code: "x_failed", Reason: "读不到服务器列表", Suggestion: "检查数据目录", Detail: "open: denied"}
	tt.Frame()
	if !tt.HasText("读不到服务器列表") || !tt.HasText("检查数据目录") || tt.HasText("open: denied") {
		t.Fatalf("%q", tt.Texts())
	}
	if err := tt.Click("技术信息"); err != nil || !tt.HasText("x_failed\nopen: denied") {
		t.Fatalf("details: %v %q", err, tt.Texts())
	}
}

// The route links stops only by recorded migrations: a migration made while
// the source was unreachable starts at “来源未知”; an agent that never
// migrated has no route.
func TestRouteFollowsRelations(t *testing.T) {
	ref := func(uid string) identity.AgentRef {
		return identity.AgentRef{ServerUID: uid, InstallID: "oc", InstanceID: "oc", Generation: "g-" + uid}
	}
	a, b, x := ref("a"), ref("b"), ref("x")
	items := []service.AgentSummary{
		{ConnectionID: "home", AgentRef: &a, Instance: enroll.InstanceSummary{InstanceID: "oc"},
			MigratedTo: &service.AgentMigration{ConnectionID: "cloud", AgentRef: b}},
		{ConnectionID: "cloud", AgentRef: &b, Instance: enroll.InstanceSummary{InstanceID: "oc"},
			Relations: []identity.RelationSummary{{Kind: identity.MigrationCompleted, SourceAgent: &a, TargetAgent: b}}},
		{ConnectionID: "debian", AgentRef: &x, Instance: enroll.InstanceSummary{InstanceID: "oc"},
			Relations: []identity.RelationSummary{{Kind: identity.MigrationCompleted, TargetAgent: x}}},
		{ConnectionID: "solo", Instance: enroll.InstanceSummary{InstanceID: "oc"}},
	}
	labels := func(st []station) (out []string) {
		for _, s := range st {
			out = append(out, s.Label)
		}
		return out
	}
	if got := labels(route(items, "cloud", "oc")); fmt.Sprint(got) != "[home cloud]" {
		t.Fatalf("cloud: %v", got)
	}
	if got := labels(route(items, "home", "oc")); fmt.Sprint(got) != "[home cloud]" {
		t.Fatalf("home: %v", got)
	}
	if st := route(items, "debian", "oc"); len(st) != 2 || !st[0].Unknown || st[0].Label != "来源未知" || !st[1].Current {
		t.Fatalf("lost: %+v", st)
	}
	if st := route(items, "solo", "oc"); st != nil {
		t.Fatalf("solo: %+v", st)
	}
}

// A click on the gold line lights the point; its details open after the
// glow's moment, or at once when the desktop asks for less motion.
func TestCurveGlowThenDetails(t *testing.T) {
	for _, still := range []bool{false, true} {
		a, tt := world(t)
		tt.SetPreferences(ui.Preferences{ReduceMotion: still})
		a.openAgent("home", "xiaoman")
		a.agentTab = 1
		tt.Frame()
		tt.Advance(time.Second)
		if err := tt.Click("昨天 07:00"); err != nil { // the first match is the card on the line
			t.Fatal(err)
		}
		tt.Advance(100 * time.Millisecond)
		if !still && (a.glow != "sb" || a.point != "") {
			t.Fatalf("glow %q point %q right after the click", a.glow, a.point)
		}
		tt.Advance(time.Second)
		if a.point != "sb" || !tt.HasText("这次带上了什么") {
			t.Fatalf("still=%v: point %q %q", still, a.point, tt.Texts())
		}
	}
}
