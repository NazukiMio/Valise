package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"valise/core/enroll"
	"valise/core/profile"
)

// Every card in a view has the same size whatever the name, icon or state
// (docs/UI.md 8.1).
func TestCardSizes(t *testing.T) {
	m, _ := world(t)
	drive(t, m, m.Init())
	d := m.server("box")
	long := strings.Repeat("很长的实例名字", 6)
	ins := []enroll.InstanceSummary{
		d.instances[0],
		{InstanceID: long, ProfileID: "unknown", Service: []enroll.ServiceState{{LoadState: "loaded", Active: "failed", Unit: "x"}},
			Entries: []enroll.EntryState{{ID: "web", Port: 1, Listening: false}}},
		{InstanceID: "old", ProfileID: "demo", Superseded: &enroll.Supersession{By: "new"}},
	}
	big := &profile.Display{Color: "#112233", Icon: strings.Repeat(strings.Repeat("⣿", 24)+"\n", 12)}
	m.prof.ByID["demo"].Effective.Display = big
	for _, compact := range []bool{false, true} {
		w, h := cardW, cardH
		if compact {
			w, h = compactCardW, compactCardH
		}
		for i, in := range ins {
			c := instanceCard(m, d, in, i == 0, compact)
			if lipgloss.Width(c) != w || lipgloss.Height(c) != h {
				t.Fatalf("compact=%v card %d is %d×%d, want %d×%d:\n%s", compact, i, lipgloss.Width(c), lipgloss.Height(c), w, h, c)
			}
		}
	}
	for _, c := range []struct{ width, per int }{{80, 1}, {110, 2}, {160, 3}} {
		if got := perPage(c.width, cardW); got != c.per {
			t.Errorf("width %d: %d per page, want %d", c.width, got, c.per)
		}
	}
	if c, _ := movePager(0, 7, 110, cardW, "]"); c != 2 {
		t.Fatalf("] to %d", c)
	}
	if c, _ := movePager(5, 7, 110, cardW, "["); c != 2 {
		t.Fatalf("[ to %d", c)
	}
	if !compactCards(79, 30) || !compactCards(100, 23) || compactCards(80, 24) {
		t.Fatal("compact threshold")
	}
	page := pager([]string{"a", "b", "c"}, 2, 110, cardW)
	if !strings.Contains(ansiRe.ReplaceAllString(page, ""), "2/2") {
		t.Fatalf("page dots: %q", page)
	}
}

// The manage tab groups actions by risk; read actions run at once, the
// others are confirmed; config ticks the escape pod; danger needs its name.
func TestManageActions(t *testing.T) {
	m, _ := world(t)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 44})
	openInstance(t, m)
	key(t, m, "2")
	wantText(t, m, "动作 · 查看", "打招呼", "日常钥匙", "动作 · 常规管理", "重启演示", "动作 · 修改配置", "换令牌", "原生", "打开这台服务器的终端")
	v := screenText(m)
	if strings.Contains(v, "/bin/echo") {
		t.Fatal("commands shown before v")
	}
	key(t, m, "v")
	wantText(t, m, "实际命令：/bin/echo hi")
	// Down to 打招呼 (after the two management-page rows... no entry here,
	// so the local page row comes first).
	for !strings.Contains(lineAt(m, "▸"), "打招呼") {
		key(t, m, "down")
	}
	key(t, m, "enter")
	wantText(t, m, "命令：退出码 0", "命令输出", "hi")
	key(t, m, "esc")
	key(t, m, "down")
	key(t, m, "enter")
	wantText(t, m, "会发生什么：重启", "需要管理登录")
	key(t, m, "enter")
	wantText(t, m, "命令：退出码 0", "无法检查运行状态")
	key(t, m, "esc")
	key(t, m, "down")
	key(t, m, "enter")
	// config needs an escape pod kept elsewhere (docs/INSTALL.md 4.2).
	if !strings.Contains(m.flash, "放在别处") {
		t.Fatalf("config action without an offsite pod: %q", m.flash)
	}
	m.svc.OwnerAuth.Passphrase = func(string) (string, error) { return pass, nil }
	if _, err := m.svc.RepoAdd("usb", filepath.Join(t.TempDir(), "usb")); err != nil {
		t.Fatal(err)
	}
	if err := m.svc.ServerNameSet(m.ctx, "box", "box"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.svc.RepoSync(m.ctx, "box", "usb"); err != nil {
		t.Fatal(err)
	}
	key(t, m, "enter")
	wantText(t, m, "能否撤销：无法撤销", "[x] 执行前先建立逃生舱")
	key(t, m, "space")
	wantText(t, m, "[ ] 不建逃生舱")
	key(t, m, "esc")
	key(t, m, "down")
	key(t, m, "enter")
	wantText(t, m, "高风险", "输入动作名称「清空演示」")
	key(t, m, "enter")
	if !strings.Contains(m.flash, "名称不一致") {
		t.Fatalf("danger ran without its name: %q", m.flash)
	}
	typeText(t, m, "清空演示")
	key(t, m, "enter")
	wantText(t, m, "命令：退出码 0")
	key(t, m, "esc")
	// 80×24: the list scrolls and the cursor stays on screen.
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	for i := 0; i < 12; i++ {
		key(t, m, "down")
		if lineAt(m, "▸") == "" {
			t.Fatalf("cursor left the screen after %d downs:\n%s", i+1, screenText(m))
		}
	}
}

func lineAt(m *Model, marker string) string {
	for _, l := range strings.Split(screenText(m), "\n") {
		if strings.Contains(l, marker) {
			return l
		}
	}
	return ""
}
