package gui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"valise/client/config"
)

func TestSettingsConfigExportAndImport(t *testing.T) {
	// Source machine with two servers (1 valise, 1 own-ssh).
	a, _ := world(t)
	if err := config.SaveServerList(a.svc.Dirs.Data, []config.Server{
		{Name: "box", Source: config.SourceValise, HostName: "10.0.0.10", User: "ubuntu", Login: config.LoginPassword},
		{Name: "myssh", Source: config.SourceSSHConfig},
	}); err != nil {
		t.Fatal(err)
	}
	exportDir := t.TempDir()
	a.SetFolderPicker(func(string, string) (string, error) { return exportDir, nil })
	a.SetFilePicker(func(string, string) (string, error) { return "", nil })

	// Render Settings Tab 0 and export.
	a.setTab = 0
	ttA := ui.NewTester(a.View, 1180, 900)
	a.router.Push("/settings")
	ttA.Frame()
	hasAll(t, ttA.Texts(), T("cfgx.title"), T("cfgx.export_open"), T("cfgx.import_open"))
	click(t, ttA, T("cfgx.export_open"))
	res := a.cfgTransferState().exportRes
	if res == nil || res.Servers != 2 {
		t.Fatalf("exportRes = %+v, err = %v", res, a.cfgTransferState().err)
	}
	raw, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"box"`) || strings.Contains(string(raw), "PRIVATE KEY") {
		t.Fatalf("unexpected export contents: %s", raw)
	}
	if !ttA.HasText(T("cfgx.export_note")) {
		t.Fatalf("missing export note: %q", ttA.Texts())
	}

	// Destination machine imports file: myssh is added, existing box conflicts and is skipped.
	b, _ := world(t)
	b.svc.Config.SSHBinary = "/usr/bin/false"
	ttB := ui.NewTester(b.View, 1180, 1400)
	if err := config.SaveServerList(b.svc.Dirs.Data, []config.Server{
		{Name: "box", Source: config.SourceValise, HostName: "10.0.0.99", User: "other", Login: config.LoginPassword},
	}); err != nil {
		t.Fatal(err)
	}
	b.SetFilePicker(func(string, string) (string, error) { return res.Path, nil })
	b.router.Push("/settings")
	ttB.Frame()
	click(t, ttB, T("cfgx.import_open"))
	hasAll(t, ttB.Texts(),
		T("cfgx.imported_title"),
		T("cfgx.added", "list", "myssh"),
		T("cfgx.skipped", "list", "box"),
		T("cfgx.need_ssh", "list", "myssh"),
	)

	// Fresh machine C gets box added and offers rekey jump straight to server settings tab.
	c, _ := world(t)
	c.svc.Config.SSHBinary = "/usr/bin/false"
	ttC := ui.NewTester(c.View, 1180, 1400)
	c.SetFolderPicker(func(string, string) (string, error) { return exportDir, nil })
	c.SetFilePicker(func(string, string) (string, error) { return res.Path, nil })
	c.router.Push("/settings")
	ttC.Frame()
	click(t, ttC, T("cfgx.import_open"))
	hasAll(t, ttC.Texts(),
		T("cfgx.need_key", "host", "box"),
		T("cfgx.rekey_go"),
	)

	// Jump to rekey takes us to /servers/box tab 1.
	click(t, ttC, T("cfgx.rekey_go"))
	if c.router.Path() != "/servers/box" || c.serverTab != 1 {
		t.Fatalf("unexpected destination: path=%s tab=%d", c.router.Path(), c.serverTab)
	}

	// Second import shows Same.
	c.router.Push("/settings")
	ttC.Frame()
	click(t, ttC, T("cfgx.import_open"))
	hasAll(t, ttC.Texts(), T("cfgx.added_none"), T("cfgx.same", "list", "box、myssh"))

	// Invalid JSON shows error cleanly.
	bad := filepath.Join(exportDir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{"format":"other"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c.SetFilePicker(func(string, string) (string, error) { return bad, nil })
	c.router.Push("/settings")
	ttC.Frame()
	click(t, ttC, T("cfgx.import_open"))
	if c.cfgTransferState().err == nil || !ttC.HasText("不是 Valise 导出的配置文件") {
		t.Fatalf("expected invalid format error: %v %q", c.cfgTransferState().err, ttC.Texts())
	}
}
