package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"valise/ui/service"
)

// An agent shows as 「名字 · 实例 id」: the harness's name until the user
// gives one (docs/UI.md 9.5 "名字").
func TestAgentNames(t *testing.T) {
	m, _ := world(t)
	prof := filepath.Join(m.svc.Dirs.Data, "profiles", "demo.yaml")
	named := demoProfile + "identity: { argv: [/bin/echo, '[{\"identityName\":\"小助手\",\"isDefault\":true},{\"identityName\":\"测试台\",\"isDefault\":false}]'], pick: { isDefault: \"true\" }, name: identityName }\n"
	if err := os.WriteFile(prof, []byte(named), 0o644); err != nil {
		t.Fatal(err)
	}
	m.prof, _ = m.svc.Profiles()
	if _, err := m.svc.Enroll(context.Background(), "box", service.EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatal(err)
	}
	drive(t, m, m.Init())
	wantText(t, m, "小助手 · demo")
	if err := m.svc.SetAgentLabel(context.Background(), "box", "demo", "我的助手"); err != nil {
		t.Fatal(err)
	}
	drive(t, m, m.loadServer("box"))
	key(t, m, "enter")
	v := wantText(t, m, "我的助手 · demo")
	if strings.Contains(v, "小助手") {
		t.Fatalf("harness name shown over the user's:\n%s", v)
	}
	key(t, m, "enter")
	key(t, m, "6")
	wantText(t, m, "名字", "我的助手", "2 个 agent：小助手、测试台")
}
