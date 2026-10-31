package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

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
	case nil:
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

func screenText(m *Model) string { return m.View().Content }

func TestOverviewInstanceAndSeal(t *testing.T) {
	m, _ := world(t)
	drive(t, m, m.Init())
	v := screenText(m)
	for _, want := range []string{"box", "demo", "未核实", "封存", "soul"} {
		if !strings.Contains(v, want) {
			t.Fatalf("overview lacks %q:\n%s", want, v)
		}
	}
	key(t, m, "down") // the instance line
	key(t, m, "enter")
	if !strings.Contains(screenText(m), "profile demo v1") {
		t.Fatalf("instance screen:\n%s", screenText(m))
	}
	// Seal the soul tier: pick, confirm.
	key(t, m, "f")
	key(t, m, "enter")
	if !strings.Contains(screenText(m), "[y/N]") {
		t.Fatalf("no confirmation:\n%s", screenText(m))
	}
	key(t, m, "y")
	if !strings.Contains(m.flash, "已封存") || m.flashErr {
		t.Fatalf("flash %q", m.flash)
	}
	// Checkup, then back to the instance.
	key(t, m, "c")
	if !strings.Contains(screenText(m), "没有发现问题") {
		t.Fatalf("checkup:\n%s", screenText(m))
	}
	key(t, m, "esc")
	key(t, m, "k")
	if !strings.Contains(screenText(m), "技能目录") && !strings.Contains(screenText(m), "没有声明技能目录") {
		t.Fatalf("kit:\n%s", screenText(m))
	}
}

func TestReviveWizardRollback(t *testing.T) {
	m, home := world(t)
	drive(t, m, m.Init())
	os.WriteFile(filepath.Join(home, ".demo/memory/a.md"), []byte("changed after the seal"), 0o644)
	key(t, m, "enter") // server header → vault
	if !strings.Contains(screenText(m), "s_") {
		t.Fatalf("vault:\n%s", screenText(m))
	}
	key(t, m, "enter") // the snapshot → wizard
	key(t, m, "d")     // allow the draft profile
	key(t, m, "enter") // passphrase prompt
	if !strings.Contains(screenText(m), "口令") {
		t.Fatalf("no passphrase prompt:\n%s", screenText(m))
	}
	typeText(t, m, pass)
	if strings.Contains(screenText(m), pass) {
		t.Fatal("passphrase echoed")
	}
	key(t, m, "enter")
	v := screenText(m)
	if !strings.Contains(v, "将恢复") || strings.Contains(v, "无法执行") {
		t.Fatalf("plan:\n%s", v)
	}
	key(t, m, "enter") // asks to stage
	// The revive runs in the background and waits for the swap answer, as
	// under the real runtime: deliver its messages through a channel.
	msgs := make(chan tea.Msg, 4)
	m.send = func(msg tea.Msg) { msgs <- msg }
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	go func() { msgs <- cmd() }()
	m.Update(<-msgs) // stageVerified
	if !strings.Contains(screenText(m), "暂存校验通过") {
		t.Fatalf("no swap question:\n%s", screenText(m))
	}
	m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m.Update(<-msgs) // reviveFinished
	if v := screenText(m); !strings.Contains(v, "已恢复") && !strings.Contains(v, "复活完成") {
		t.Fatalf("result:\n%s", v)
	}
	if b, _ := os.ReadFile(filepath.Join(home, ".demo/memory/a.md")); string(b) != "m" {
		t.Fatalf("memory not rolled back: %q", b)
	}
}

func TestProposalsEmptyAndSettings(t *testing.T) {
	m, _ := world(t)
	drive(t, m, m.Init())
	key(t, m, "s")
	if !strings.Contains(screenText(m), "指纹") {
		t.Fatalf("settings:\n%s", screenText(m))
	}
	key(t, m, "esc")
	key(t, m, "down")
	key(t, m, "enter")
	key(t, m, "p")
	if !strings.Contains(screenText(m), "没有待确认的归类提议") {
		t.Fatalf("proposals:\n%s", screenText(m))
	}
}
