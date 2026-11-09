package gui

import (
	"context"
	"fmt"
	"strings"
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
	// Today at 09:00, so that “今天” and “昨天” hold whenever tests run.
	y, m, d := time.Now().Date()
	at := time.Date(y, m, d, 9, 0, 0, 0, time.Local)
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

// The home page answers first: a verdict, then only the things that need
// attention, each with “知道了” and “带我去处理”; agents are cards with one
// quiet line (docs/UI.md 7.2, 7.3).
func TestHomePutsAttentionFirst(t *testing.T) {
	a, tt := world(t)
	for _, want := range []string{"2 个 Agent，2 件事需要你处理", "nas 离线", "连不上", "阿蓝 服务已停止", "知道了", "带我去处理",
		"运行中 · 今天 09:00 备份到本机", "在用的 Agent · 2", "卡片视图", "列表视图"} {
		if !tt.HasText(want) {
			t.Errorf("missing %q in %q", want, tt.Texts())
		}
	}
	// A fine agent's facts are not laid out as a board on the home page.
	if tt.HasText("管理页端口") || tt.HasText("工具箱") {
		t.Errorf("board or unimplemented entry drawn: %q", tt.Texts())
	}
	// The first card's step opens the server's page.
	click(t, tt, "带我去处理")
	if a.router.Path() != "/servers/nas" || !tt.HasText("连不上") || !tt.HasText("重新连接") {
		t.Fatalf("next step went to %s: %q", a.router.Path(), tt.Texts())
	}
}

// “知道了” stops a reminder in its current state, on this computer; a
// changed situation brings it back, and it can be asked back by hand.
func TestDismissRemembersTheSituation(t *testing.T) {
	a, tt := world(t)
	click(t, tt, "知道了")
	if !tt.HasText("2 个 Agent，1 件事需要你处理") || !tt.HasText("另有 1 件已知道 ›") || tt.HasText("nas 离线") {
		t.Fatalf("%q", tt.Texts())
	}
	b := New(a.svc)
	b.loadPrefs()
	if b.prefs.Dismissed["srv-nas"] != "unknown:ssh_connect_failed" {
		t.Fatalf("not kept on disk: %v", b.prefs.Dismissed)
	}
	click(t, tt, "另有 1 件已知道 ›")
	click(t, tt, "重新提醒")
	if !tt.HasText("nas 离线") || tt.HasText("另有 1 件已知道 ›") {
		t.Fatalf("not asked back: %q", tt.Texts())
	}
	click(t, tt, "知道了")
	a.agents.value.Observations[2].Error.Code = "host_key_changed"
	tt.Frame()
	if !tt.HasText("nas 需要处理") {
		t.Fatalf("a changed situation stayed hidden: %q", tt.Texts())
	}
}

// The service reminder leads to the agent's health tab, where the
// harness's declared repair action is offered.
func TestServiceReminderOpensHealth(t *testing.T) {
	a, tt := world(t)
	a.health["cloud/alan"] = &healthState{at: time.Now(), report: &service.HealthReport{Items: []service.HealthItem{
		{ID: "service", Level: service.HealthBad, Title: "服务没有运行", ActionID: "start", ActionName: "启动服务"},
		{ID: "pods", Level: service.HealthOK, Title: "备份正常"}}}}
	for _, it := range a.attentionItems(a.agentRows(a.agents.value), time.Now()) {
		if it.Key == "svc-cloud/alan" {
			a.goHandle(it)
		}
	}
	tt.Advance(time.Second)
	if a.router.Path() != "/agents/cloud/alan" || a.agentTab != tabHealth {
		t.Fatalf("went to %s tab %d", a.router.Path(), a.agentTab)
	}
	for _, want := range []string{"服务没有运行", "启动服务", "备份正常", "重新检查"} {
		if !tt.HasText(want) {
			t.Errorf("missing %q in %q", want, tt.Texts())
		}
	}
}

// The search filters by name, harness and server; the view choice is kept.
func TestSearchAndViewChoice(t *testing.T) {
	a, tt := world(t)
	a.query = "hermes"
	tt.Frame()
	if !tt.HasText("阿蓝") || tt.HasText("小满") || !tt.HasText("找到 1 个 · 共 2 个在用") {
		t.Fatalf("search: %q", tt.Texts())
	}
	a.query = "HOME"
	tt.Frame()
	if tt.HasText("阿蓝 的图标") || !tt.HasText("小满") {
		t.Fatalf("search by server: %q", tt.Texts())
	}
	a.query = "nobody"
	tt.Frame()
	if !tt.HasText("没有找到“nobody”。可以按名字、harness 或服务器搜索。") {
		t.Fatalf("no match: %q", tt.Texts())
	}
	a.query = ""
	click(t, tt, "列表视图")
	b := New(a.svc)
	b.loadPrefs()
	if a.prefs.View != "list" || b.prefs.View != "list" {
		t.Fatalf("view not kept: %q %q", a.prefs.View, b.prefs.View)
	}
	if !tt.HasText("OpenClaw · home") {
		t.Fatalf("list view: %q", tt.Texts())
	}
}

// Local copies count only when a repository is registered; stale ones are
// one card, not one per agent. Retired agents are folded away and never
// count.
func TestStaleCopiesAndRetired(t *testing.T) {
	a, tt := world(t)
	a.hasRepos = true
	a.agents.value.Items[1].MigratedTo = &service.AgentMigration{ConnectionID: "home"}
	tt.Frame()
	// 小满 was backed up today: nothing stale yet.
	if !tt.HasText("1 个 Agent，1 件事需要你处理") || tt.HasText("1 个 Agent 超过 7 天没有新备份到这台电脑") {
		t.Fatalf("%q", tt.Texts())
	}
	if tt.HasText("阿蓝 服务已停止") || !tt.HasText("不再使用 · 1（已迁出或已被替换）") {
		t.Fatalf("retired agent counted: %q", tt.Texts())
	}
	a.timeline.value.Items = nil
	tt.Frame()
	// The server was not read about its vault: no cause, no card.
	if !tt.HasText("1 个 Agent，1 件事需要你处理") {
		t.Fatalf("judged without the server: %q", tt.Texts())
	}
	// The server has a newer backup: copy it here.
	a.vaults = map[string]vaultRead{"home": {newest: map[string]time.Time{"xiaoman": time.Now().Add(-time.Hour)}}}
	tt.Frame()
	if !tt.HasText("1 个 Agent 有新备份还没复制到这台电脑") || tt.HasText("1 个 Agent 超过 7 天没有新备份") {
		t.Fatalf("to copy: %q", tt.Texts())
	}
	// The server has none either: look at the scheduled backups.
	a.vaults = map[string]vaultRead{"home": {newest: map[string]time.Time{}}}
	tt.Frame()
	if !tt.HasText("1 个 Agent 超过 7 天没有新备份") || tt.HasText("1 个 Agent 有新备份还没复制到这台电脑") {
		t.Fatalf("none on the server: %q", tt.Texts())
	}
	for _, it := range a.attentionItems(a.agentRows(a.agents.value), time.Now()) {
		if it.Key == "nobackup" && (it.Go != "/agents/home/xiaoman" || it.Tab != tabHealth) {
			t.Fatalf("nobackup leads to %s tab %d", it.Go, it.Tab)
		}
	}
}

// The agent page's one primary action opens the admin page through a
// tunnel; each step is reported apart and the token never shows.
func TestOpenEntrySteps(t *testing.T) {
	a, tt := world(t)
	var opened string
	a.openURL = func(u string) error { opened = u; return nil }
	a.entryOpener = func(ctx context.Context, server, instance string, o service.OpenOptions) error {
		o.Status(service.EntryStatus{Step: service.EntryTunnel, OK: true, Detail: "本机 → 服务器"})
		o.Status(service.EntryStatus{Step: service.EntryRemote, OK: false, Detail: "服务器上没有程序监听端口 18789"})
		o.Status(service.EntryStatus{Step: service.EntryAuth, OK: true, Detail: "secret-token"})
		o.Ready("http://127.0.0.1:18789/#token=secret-token", "http://127.0.0.1:18789/")
		return nil
	}
	a.run = func(f func()) { f() }
	click(t, tt, "小满")
	click(t, tt, "打开管理页")
	if opened != "http://127.0.0.1:18789/#token=secret-token" {
		t.Fatalf("browser got %q", opened)
	}
	if !tt.HasText("隧道已建立") || !tt.HasText("服务器端口在监听") || !tt.HasText("服务器上没有程序监听端口 18789") {
		t.Fatalf("%q", tt.Texts())
	}
	for _, x := range tt.Texts() {
		if strings.Contains(x, "secret-token") {
			t.Fatalf("token shown: %q", x)
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

// A server's vault may keep its folder under another name than the
// connection's (“debian-vm” → “debian”); backups are found by that name.
func TestPointsByVaultName(t *testing.T) {
	a, _ := world(t)
	a.timeline.value.Items = append(a.timeline.value.Items, journey.TimelinePoint{SnapshotRef: journey.SnapshotRef{SnapshotID: "d1"},
		CreatedAt: time.Now(), RecordedInstanceID: "alan", Copies: []journey.Copy{{SourceLocator: "cloudbox/alan/2026-11-01_2026-11-30"}}})
	if len(a.pointsOf("cloud", "alan")) != 0 {
		t.Fatal("matched before the name was known")
	}
	a.vaultNames["cloud"] = "cloudbox"
	if got := a.pointsOf("cloud", "alan"); len(got) != 1 || got[0].SnapshotRef.SnapshotID != "d1" {
		t.Fatalf("by vault name: %v", got)
	}
}
