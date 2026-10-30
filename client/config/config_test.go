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
