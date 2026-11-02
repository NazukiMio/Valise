package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"valise/ui/service"
)

// TestInstallPage: an installation with several agents is one card; its
// page lists enrolled and detected agents in the harness's own words.
func TestInstallPage(t *testing.T) {
	m, home := world(t)
	prof := filepath.Join(m.svc.Dirs.Data, "profiles", "demo.yaml")
	named := strings.Replace(demoProfile, "markers_any: [demo.json] }",
		`markers_any: [demo.json], named: [ { glob: "${HOME}/.demo-*", env: { DEMO_HOME: "${STATE_DIR}" } } ] }`+"\nterms: { agent: profile }", 1)
	if err := os.WriteFile(prof, []byte(named), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"two", "three"} {
		os.MkdirAll(filepath.Join(home, ".demo-"+n), 0o755)
		os.WriteFile(filepath.Join(home, ".demo-"+n, "demo.json"), []byte("{}"), 0o644)
	}
	if _, err := m.svc.Enroll(context.Background(), "box", service.EnrollOptions{ProfileID: "demo", Name: "two", Accept: true}); err != nil {
		t.Fatal(err)
	}
	m.prof, _ = m.svc.Profiles()
	drive(t, m, m.Init())
	key(t, m, "enter")
	wantText(t, m, "Demo", "2 个 profile", "· 默认 profile", "· profile two")
	key(t, m, "enter")
	wantText(t, m, "1 agent", "2 管理", "3 健康", "4 设置", "默认 profile", "profile two", "profile three", "还没接入 Valise")
	key(t, m, "down")
	key(t, m, "enter")
	v := wantText(t, m, "1 概览", "2 健康", "3 逃生舱", "4 设置")
	if strings.Contains(v, "管理页、服务") || strings.Contains(v, "5 ") {
		t.Fatalf("agent page:\n%s", v)
	}
}
