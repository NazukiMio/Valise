package config

import (
	"os"
	"path/filepath"
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
