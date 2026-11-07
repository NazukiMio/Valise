package main

import (
	"strings"
	"testing"

	"valise/client/config"
	"valise/ui/service"
)

func TestConfigTakeover(t *testing.T) {
	cfg := t.TempDir()
	if _, err := (&service.Service{Dirs: config.Dirs{Data: cfg}}).AddServers("box"); err != nil {
		t.Fatal(err)
	}
	if c, _ := config.Load(cfg); c.Takeover["box"] {
		t.Fatal("on by default")
	}
	out, errOut, code := runCLI(t, cfg, "", "config", "takeover", "box", "on")
	if code != 0 || !strings.Contains(out, "box 的接管模式已开启") || !strings.Contains(out, "sudo -n") {
		t.Fatalf("code=%d\n%s\n%s", code, out, errOut)
	}
	if c, _ := config.Load(cfg); !c.Takeover["box"] {
		t.Fatal("not saved")
	}
	out, _, code = runCLI(t, cfg, "", "config", "takeover", "box", "off")
	if code != 0 || !strings.Contains(out, "转到终端执行") {
		t.Fatal(out)
	}
	if c, _ := config.Load(cfg); c.Takeover["box"] {
		t.Fatal("still on")
	}
	if _, _, code = runCLI(t, cfg, "", "config", "takeover", "box", "maybe"); code == 0 {
		t.Fatal("bad state accepted")
	}
	if _, _, code = runCLI(t, cfg, "", "config", "takeover", "nowhere", "on"); code == 0 {
		t.Fatal("unknown server accepted")
	}
	if _, errOut, _ := runCLI(t, cfg, "", "config", "takeover", "box"); !strings.Contains(errOut, "config takeover <服务器> on|off") {
		t.Fatal("usage line missing")
	}
}
