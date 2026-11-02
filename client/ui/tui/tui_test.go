package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"valise/client/config"
	"valise/core/vault"
	"valise/core/vaultrpc"
	"valise/testbed/fakesshenv"
	"valise/ui/service"
)

// Harness names in tests are fixtures only.
const demoProfile = `schema: valise-profile/1
id: demo
display_name: Demo
profile_version: 1
status: draft
detect: { binaries: [demo], state_dir_candidates: ["${HOME}/.demo"], markers_any: [demo.json] }
paths:
  - { root: state, glob: ".env", category: secrets }
  - { root: state, glob: "demo.json", category: config }
  - { root: state, glob: "memory/**", category: memory }
provenance: { kind: binary, binary: { name: demo } }
actions:
  - { id: hello, name: 打招呼, explain: "输出 hi", risk: read, argv: [/bin/echo, hi], output: text }
  - { id: restart, name: 重启演示, explain: "重启", risk: manage, argv: [/bin/echo, ok], output: none }
  - { id: rotate, name: 换令牌, explain: "换一个令牌", undo: "无法撤销", risk: config, argv: [/bin/echo, x], output: none }
  - { id: wipe, name: 清空演示, explain: "清空", undo: "无法撤销", risk: danger, argv: [/bin/echo, y], output: none }
checkup:
  memory_files: [ { root: state, glob: "memory/a.md", warn_lines: 100 } ]
  memory_mechanism_markers: [ { root: state, glob: "memory/*.md" } ]
`

const pass = "correct horse battery"

// world is a fake server "box" with an enrolled, sealed demo instance.
func world(t *testing.T) (*Model, string) {
	t.Helper()
	e := fakesshenv.New(t)
	home := e.AddHost("box")
	e.InstallPorter("box")
	for rel, body := range map[string]string{
		".demo/demo.json": "{}", ".demo/.env": "K=v", ".demo/memory/a.md": "m",
		".local/bin/demo": "#!/bin/sh\necho demo 1.0\n",
	} {
		p := filepath.Join(home, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o755)
	}
	dir := t.TempDir()
	svc := &service.Service{Dirs: config.Dirs{Data: filepath.Join(dir, "data"), Cache: filepath.Join(dir, "cache")},
		Config: config.Config{SSHBinary: e.SSH}, Version: "test", Home: dir, SSHConfigPath: filepath.Join(dir, "ssh_config")}
	os.MkdirAll(filepath.Join(svc.Dirs.Data, "profiles"), 0o755)
	os.WriteFile(filepath.Join(svc.Dirs.Data, "profiles", "demo.yaml"), []byte(demoProfile), 0o644)
	ctx := context.Background()
	if _, err := svc.KeyInit(pass); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Enroll(ctx, "box", service.EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.KeyRegister(ctx, "box", "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Seal(ctx, "box", vaultrpc.SealParams{InstanceID: "demo", Tier: vault.TierSoul}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddServers("box"); err != nil {
		t.Fatal(err)
	}
	m := New(ctx, Options{Service: svc})
	m.send = func(msg tea.Msg) { m.Update(msg) }
	m.tick = 0
	return m, home
}

// drive runs a command and feeds every resulting message back, the way the
// Bubble Tea runtime does.
func drive(t *testing.T, m *Model, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	msg := cmd()
	if strings.HasPrefix(fmt.Sprintf("%T", msg), "cursor.") {
		return // cursor blinking: a timer loop that the runtime handles
	}
	switch msg := msg.(type) {
	case nil, adminTick, animTick:
		// adminTick: the top bar's 30 s redraw loop, handled by the runtime
	case tea.BatchMsg:
		for _, c := range msg {
			drive(t, m, c)
		}
	default:
		_, next := m.Update(msg)
		drive(t, m, next)
	}
}

func key(t *testing.T, m *Model, k string) {
	t.Helper()
	var msg tea.KeyPressMsg
	switch k {
	case "enter":
		msg = tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		msg = tea.KeyPressMsg{Code: tea.KeyEscape}
	case "down":
		msg = tea.KeyPressMsg{Code: tea.KeyDown}
	case "up":
		msg = tea.KeyPressMsg{Code: tea.KeyUp}
	case "right":
		msg = tea.KeyPressMsg{Code: tea.KeyRight}
	case "tab":
		msg = tea.KeyPressMsg{Code: tea.KeyTab}
	case "end":
		msg = tea.KeyPressMsg{Code: tea.KeyEnd}
	case "space":
		msg = tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	default:
		msg = tea.KeyPressMsg{Code: rune(k[0]), Text: k}
	}
	_, cmd := m.Update(msg)
	drive(t, m, cmd)
}

func typeText(t *testing.T, m *Model, s string) {
	t.Helper()
	for _, r := range s {
		_, cmd := m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		drive(t, m, cmd)
	}
}

var ansiRe = regexp.MustCompile("\x1b\\[[0-9;:]*m")

func screenText(m *Model) string { return ansiRe.ReplaceAllString(m.View().Content, "") }

func wantText(t *testing.T, m *Model, wants ...string) string {
	t.Helper()
	v := screenText(m)
	for _, w := range wants {
		if !strings.Contains(v, w) {
			t.Fatalf("screen lacks %q:\n%s", w, v)
		}
	}
	return v
}

// openInstance goes home → box → demo.
func openInstance(t *testing.T, m *Model) {
	t.Helper()
	drive(t, m, m.Init())
	wantText(t, m, "box", "1 个 agent", "在线")
	key(t, m, "enter")
	wantText(t, m, "demo", "Demo", "harness 资料未核实")
	key(t, m, "enter")
	wantText(t, m, "1 概览", "2 管理", "3 健康", "4 逃生舱", "5 设置")
}

func TestHomeServerInstanceAndSeal(t *testing.T) {
	m, _ := world(t)
	openInstance(t, m)
	wantText(t, m, "需要处理：无", "快捷操作", "建立逃生舱", "支持程度", "备份")
	// Build an escape pod: what to pack, then why.
	key(t, m, "b")
	wantText(t, m, "轻装", "标准", "完整", "原样")
	key(t, m, "enter")
	typeText(t, m, "升级前")
	key(t, m, "enter")
	wantText(t, m, "逃生舱建好了", "轻装")
	key(t, m, "enter")
	if !strings.Contains(m.flash, "逃生舱已建立") || m.flashErr {
		t.Fatalf("flash %q", m.flash)
	}
	key(t, m, "4")
	wantText(t, m, "你手动建立", "升级前", "轻装", "共 2 个逃生舱")
	// Search narrows the list.
	key(t, m, "/")
	typeText(t, m, "升级")
	wantText(t, m, "1 / 2")
	key(t, m, "enter")
	key(t, m, "enter")
	wantText(t, m, "的逃生舱", "谁建立的", "你手动建立", "原因", "升级前")
	if strings.Contains(screenText(m), "s_") {
		t.Fatal("snapshot id shown before the technical details are opened")
	}
	key(t, m, "t")
	wantText(t, m, "s_", "技术信息")
}

func TestLandingRollback(t *testing.T) {
	m, home := world(t)
	openInstance(t, m)
	os.WriteFile(filepath.Join(home, ".demo/memory/a.md"), []byte("changed after the seal"), 0o644)
	key(t, m, "4")
	key(t, m, "enter")
	key(t, m, "l")
	wantText(t, m, "逃生舱着陆", "原地着陆")
	key(t, m, "d")     // allow the draft profile
	key(t, m, "enter") // passphrase prompt
	wantText(t, m, "口令")
	typeText(t, m, pass)
	if strings.Contains(screenText(m), pass) {
		t.Fatal("passphrase echoed")
	}
	key(t, m, "enter")
	v := wantText(t, m, "将放回")
	if strings.Contains(v, "无法执行") {
		t.Fatalf("plan blocked:\n%s", v)
	}
	msgs := make(chan tea.Msg, 4)
	m.send = func(msg tea.Msg) { msgs <- msg }
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}) // stage
	go func() { msgs <- cmd() }()
	m.Update(<-msgs) // stageVerified
	wantText(t, m, "暂存与校验都通过了", "正在呼叫地面指挥")
	if _, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}); cmd != nil || !strings.Contains(m.flash, "不能退出") {
		t.Fatalf("ctrl+c during a landing: flash %q", m.flash)
	}
	m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m.Update(<-msgs) // reviveFinished
	if strings.Contains(screenText(m), "登陆成功") {
		t.Fatal("success shown before the rocket landed")
	}
	key(t, m, "enter") // skip the animation
	wantText(t, m, "逃生舱登陆成功！", "健康检查")
	if b, _ := os.ReadFile(filepath.Join(home, ".demo/memory/a.md")); string(b) != "m" {
		t.Fatalf("memory not rolled back: %q", b)
	}
}

// The health tab (docs/UI.md 9.5): plain words, problems first, a restore
// test in place. Regression kept: a/x with no proposals pending must not
// panic, and esc goes back.
func TestHealthTab(t *testing.T) {
	m, _ := world(t)
	openInstance(t, m)
	key(t, m, "3")
	wantText(t, m, "逃生舱状态", "agent 健康", "都只在服务器上", "还没有测试过")
	if v := screenText(m); strings.Contains(v, "着陆") || strings.Contains(v, "上云") {
		t.Fatal("themed words on the health tab")
	}
	// i dismisses the selected warn (the first problem: the backup).
	key(t, m, "i")
	wantText(t, m, "已忽略，7 天后恢复提醒")
	key(t, m, "i")
	if strings.Contains(screenText(m), "已忽略") {
		t.Fatal("dismissal not lifted")
	}
	m.svc.OwnerAuth.Passphrase = func(string) (string, error) { return pass, nil }
	key(t, m, "t")
	wantText(t, m, "上次测试", "通过")
	for _, k := range []string{"a", "x", "down", "up", "esc"} {
		key(t, m, k)
	}
	wantText(t, m, "接入 Valise") // back on the server page
}

// Regression: a list longer than the window kept rendering past it and the
// cursor left the screen.
func TestLongListsScroll(t *testing.T) {
	m, _ := world(t)
	openInstance(t, m)
	d := m.server("box")
	base := d.status.Snapshots[0]
	for i := 0; i < 80; i++ {
		s := base
		s.SnapshotID = fmt.Sprintf("s_01M6%022d", i)
		s.CreatedAt = base.CreatedAt.Add(-time.Duration(i) * 7 * time.Hour)
		d.status.Snapshots = append(d.status.Snapshots, s)
	}
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	key(t, m, "4")
	for i := 0; i < 60; i++ {
		key(t, m, "down")
	}
	v := screenText(m)
	if n := strings.Count(v, "\n") + 1; n > 24 {
		t.Fatalf("view has %d lines for a 24-line window", n)
	}
	if !strings.Contains(v, "▸") || !strings.Contains(v, "第 61 / 81 个") {
		t.Fatalf("cursor not visible:\n%s", v)
	}
	key(t, m, "end")
	wantText(t, m, "第 81 / 81 个")
}

func TestCloudCleanupServerSettings(t *testing.T) {
	m, _ := world(t)
	drive(t, m, m.Init())
	key(t, m, "enter")
	key(t, m, "c")
	wantText(t, m, "还没有仓库或空间站")
	key(t, m, "esc")
	key(t, m, "k")
	wantText(t, m, "没有可以清理的东西") // the only period is current
	key(t, m, "esc")
	key(t, m, "t")
	wantText(t, m, "服务器设置", "porter", "已登记到这台服务器")
}

func TestSettingsSections(t *testing.T) {
	m, _ := world(t)
	drive(t, m, m.Init())
	key(t, m, "s")
	wantText(t, m, "你的钥匙已创建", "备份钥匙", "只能上锁")
	// The sidebar is vertical: ↓ moves through the sections.
	for _, want := range []string{"box", "还没有空间站", "不包含", "harness 资料包", "能打开逃生舱的钥匙", "不用 Valise 也能恢复"} {
		key(t, m, "down")
		wantText(t, m, want)
	}
	// → enters the server list of 服务器; ↑↓ then move in it, ← leaves.
	key(t, m, "2")
	key(t, m, "right")
	wantText(t, m, "回到分栏")
	key(t, m, "left")
	key(t, m, "down")
	wantText(t, m, "还没有空间站")
}

func TestFirstRunGuide(t *testing.T) {
	m, _ := world(t)
	svc := m.svc
	os.Remove(filepath.Join(svc.Dirs.Data, "keystore.age"))
	os.Remove(filepath.Join(svc.Dirs.Data, "recipient.txt"))
	m = New(context.Background(), Options{Service: svc})
	wantText(t, m, "欢迎使用 Valise")
	key(t, m, "enter")
	key(t, m, "enter")
	typeText(t, m, "short")
	key(t, m, "enter")
	wantText(t, m, "至少 12 个字符")
	key(t, m, "enter")
	typeText(t, m, pass)
	key(t, m, "enter")
	typeText(t, m, pass)
	key(t, m, "enter")
	wantText(t, m, "钥匙已生成", "强烈建议现在备份")
	key(t, m, "enter") // later
	wantText(t, m, "harness 资料包")
	key(t, m, "enter") // skip
	wantText(t, m, "添加第一台服务器")
	key(t, m, "s")
	wantText(t, m, "box") // home
}

func TestIconFitting(t *testing.T) {
	braille := strings.Repeat("⣿", 30)
	var art []string
	for i := 0; i < 15; i++ {
		art = append(art, braille)
	}
	out := fitIcon(art, iconW, iconH)
	if len(out) > iconH || len([]rune(out[0])) > iconW || out[0][0] == ' ' {
		t.Fatalf("braille not scaled into the box: %d×%d", len(out), len([]rune(out[0])))
	}
	// A hollow square keeps its inside empty when scaled by dot share.
	hollow := []string{"⣿⣿⣿⣿⣿⣿", "⣿⠀⠀⠀⠀⣿", "⣿⠀⠀⠀⠀⣿", "⣿⣿⣿⣿⣿⣿"}
	small := fitIcon(hollow, 3, 2)
	if strings.ContainsRune(small[0][1:2]+small[1][1:2], '⣿') {
		t.Fatalf("inside filled in: %q", small)
	}
	dots := []string{" •●●:.        .:●●•", ":●●●●:        :●●●●:"}
	for len(dots) < 14 {
		dots = append(dots, "   ●●●●●●●●●●●●")
	}
	if out = fitIcon(dots, iconW, iconH); len(out) > iconH {
		t.Fatalf("art not sampled: %d rows", len(out))
	}
	for _, icon := range [][]string{standardIcon(nil), compactIcon(nil)} {
		w := lipgloss.Width(icon[0])
		for _, l := range icon {
			if lipgloss.Width(l) != w {
				t.Fatalf("icon box not uniform: %q", icon)
			}
		}
	}
	if len(standardIcon(nil)) != iconH || len(compactIcon(nil)) != compactIconH || lipgloss.Width(standardIcon(nil)[0]) != iconW {
		t.Fatal("robot icon not in the standard boxes")
	}
}
