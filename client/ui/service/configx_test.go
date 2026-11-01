package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"valise/client/config"
)

func TestConfigExportImport(t *testing.T) {
	a := &Service{Dirs: config.Dirs{Data: t.TempDir()}, Config: config.Config{AdminWindowMinutes: 30}}
	list := []config.Server{
		{Name: "vps", Source: config.SourceValise, HostName: "203.0.113.5", Port: 2222, User: "me", Login: config.LoginPassword},
		{Name: "home", Source: config.SourceSSHConfig},
	}
	if err := config.SaveServerList(a.Dirs.Data, list); err != nil {
		t.Fatal(err)
	}
	known := "[203.0.113.5]:2222 ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBkd\nother.example ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBkd\n"
	os.WriteFile(filepath.Join(a.Dirs.Data, config.KnownHostsFile), []byte(known), 0o600)
	os.MkdirAll(config.KeyDir(a.Dirs.Data, "vps"), 0o700)
	os.WriteFile(filepath.Join(config.KeyDir(a.Dirs.Data, "vps"), "daily"), []byte("PRIVATE KEY"), 0o600)

	out := filepath.Join(t.TempDir(), "valise-config.json")
	r, err := a.ExportConfig(out)
	if err != nil || r.Servers != 2 || r.KnownHosts != 1 {
		t.Fatalf("%+v %v", r, err)
	}
	b, _ := os.ReadFile(out)
	if strings.Contains(string(b), "PRIVATE") || strings.Contains(string(b), "other.example") {
		t.Fatalf("export holds more than the records:\n%s", b)
	}
	if _, err := a.ExportConfig(out); err == nil {
		t.Fatal("export overwrote a file")
	}

	// A new computer with one conflicting server.
	bs := &Service{Dirs: config.Dirs{Data: t.TempDir()}}
	config.SaveServerList(bs.Dirs.Data, []config.Server{{Name: "home", Source: config.SourceValise, HostName: "198.51.100.1", Login: config.LoginPassword}})
	res, err := bs.ImportConfig(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Added, ",") != "vps" || strings.Join(res.Skipped, ",") != "home" || strings.Join(res.NeedKeys, ",") != "vps" {
		t.Fatalf("%+v", res)
	}
	got, _ := config.LoadServerList(bs.Dirs.Data)
	if len(got) != 2 || got[0].HostName != "198.51.100.1" || got[1] != list[0] {
		t.Fatalf("%+v", got)
	}
	kh, _ := os.ReadFile(filepath.Join(bs.Dirs.Data, config.KnownHostsFile))
	if !strings.Contains(string(kh), "[203.0.113.5]:2222 ssh-ed25519") {
		t.Fatalf("host key not imported: %s", kh)
	}
	if bs.Config.AdminWindowMinutes != 30 {
		t.Fatalf("settings: %+v", res)
	}
	if res, _ := bs.ImportConfig(out); len(res.Added) != 0 || len(res.Same) != 1 {
		t.Fatalf("second import: %+v", res)
	}
	os.WriteFile(out, []byte(`{"servers":[]}`), 0o600)
	if _, err := bs.ImportConfig(out); err == nil {
		t.Fatal("accepted a file of another format")
	}
}
