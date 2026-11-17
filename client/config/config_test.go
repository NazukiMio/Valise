package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	c, err := Load(dir)
	if err != nil || c.SSHBinary != "ssh" {
		t.Fatalf("%+v %v", c, err)
	}
	os.WriteFile(filepath.Join(dir, ConfigFile), []byte(`{"ssh_binary":"/tmp/fakessh"}`), 0o600)
	if c, _ := Load(dir); c.SSHBinary != "/tmp/fakessh" {
		t.Fatalf("%+v", c)
	}
	os.WriteFile(filepath.Join(dir, ConfigFile), []byte(`{}`), 0o600)
	if c, _ := Load(dir); c.SSHBinary != "ssh" {
		t.Fatalf("%+v", c)
	}
}

func TestServersRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if s, err := LoadServers(dir); err != nil || len(s) != 0 {
		t.Fatalf("%v %v", s, err)
	}
	if err := SaveServers(dir, []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	if s, _ := LoadServers(dir); len(s) != 2 || s[1] != "b" {
		t.Fatalf("%v", s)
	}
}

func TestServerListFormats(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ServersFile), []byte(`{"servers":["old-alias"]}`), 0o600)
	list, err := LoadServerList(dir)
	if err != nil || len(list) != 1 || list[0].Source != SourceSSHConfig || list[0].Name != "old-alias" {
		t.Fatalf("%+v %v", list, err)
	}
	list = append(list, Server{Name: "vps", Source: SourceValise, HostName: "203.0.113.5", Port: 2222, User: "me", Login: LoginPassword})
	if err := SaveServerList(dir, list); err != nil {
		t.Fatal(err)
	}
	got, _ := LoadServerList(dir)
	if len(got) != 2 || got[1] != list[1] {
		t.Fatalf("%+v", got)
	}
	cfg, _ := os.ReadFile(filepath.Join(dir, SSHConfigFile))
	for _, want := range []string{"Host vps\n", "HostName 203.0.113.5", "Port 2222", "User me", "StrictHostKeyChecking yes", KnownHostsFile} {
		if !strings.Contains(string(cfg), want) {
			t.Errorf("ssh_config lacks %q:\n%s", want, cfg)
		}
	}
	if strings.Contains(string(cfg), "old-alias") || strings.Contains(string(cfg), "IdentityFile") {
		t.Errorf("ssh_config:\n%s", cfg)
	}
	// SaveServers keeps details of servers already recorded.
	if err := SaveServers(dir, []string{"vps", "new"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := LoadServerList(dir); len(got) != 2 || got[0].HostName != "203.0.113.5" || got[1].Source != SourceSSHConfig {
		t.Fatalf("%+v", got)
	}
}

func TestServerValidate(t *testing.T) {
	ok := Server{Name: "vps", Source: SourceValise, HostName: "example.com", Port: 22, User: "u", Login: LoginPassword}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Server{
		{Name: "-oProxyCommand=x", Source: SourceSSHConfig},
		{Name: "a b", Source: SourceSSHConfig},
		{Name: "vps", Source: SourceValise, HostName: "x\nHost *", Login: LoginPassword},
		{Name: "vps", Source: SourceValise, HostName: "x", User: "a b", Login: LoginPassword},
		{Name: "vps", Source: SourceValise, HostName: "x", Login: LoginKey, LoginKey: "rel/key"},
		{Name: "vps", Source: SourceValise, HostName: "x", Port: 70000, Login: LoginPassword},
	} {
		if bad.Validate() == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
}

func TestTakeoverAndDetached(t *testing.T) {
	dir := t.TempDir()
	if c, _ := Load(dir); c.Takeover["box"] || c.DetachedTasks {
		t.Fatalf("default not off: %+v", c)
	}
	if err := SetRedDismissDays(dir, 60); err != nil {
		t.Fatal(err)
	}
	if err := SetTakeover(dir, "box", true); err != nil {
		t.Fatal(err)
	}
	if err := SetTakeover(dir, "nas", true); err != nil {
		t.Fatal(err)
	}
	if err := SetDetachedTasks(dir, true); err != nil {
		t.Fatal(err)
	}
	c, err := Load(dir)
	if err != nil || !c.Takeover["box"] || !c.Takeover["nas"] || c.Takeover["other"] || c.RedDismissDays != 60 || !c.DetachedTasks {
		t.Fatalf("%+v %v", c, err)
	}
	if err := SetTakeover(dir, "box", false); err != nil {
		t.Fatal(err)
	}
	if c, _ := Load(dir); c.Takeover["box"] || !c.Takeover["nas"] {
		t.Fatalf("%+v", c)
	}
	if SetTakeover(dir, "", true) == nil {
		t.Fatal("empty server accepted")
	}
	b, _ := os.ReadFile(filepath.Join(dir, ConfigFile))
	if strings.Contains(string(b), `"box"`) {
		t.Fatalf("off server kept: %s", b)
	}
}

func TestDebugLogRoundTrip(t *testing.T) {
	dir := t.TempDir()
	c, err := Load(dir)
	if err != nil || c.DebugLog != nil {
		t.Fatalf("expected nil DebugLog when unset, got %+v %v", c, err)
	}
	if err := SetDetachedTasks(dir, true); err != nil {
		t.Fatal(err)
	}
	if err := SetDebugLog(dir, true); err != nil {
		t.Fatal(err)
	}
	c, err = Load(dir)
	if err != nil || c.DebugLog == nil || !*c.DebugLog || !c.DetachedTasks {
		t.Fatalf("expected DebugLog=true and preserved settings, got %+v %v", c, err)
	}
	if err := SetDebugLog(dir, false); err != nil {
		t.Fatal(err)
	}
	c, err = Load(dir)
	if err != nil || c.DebugLog == nil || *c.DebugLog || !c.DetachedTasks {
		t.Fatalf("expected DebugLog=false and preserved settings, got %+v %v", c, err)
	}
}
