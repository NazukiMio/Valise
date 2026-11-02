package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"valise/client/config"
)

// Adding a station installs the client's restricted key on it; the first
// copy gives the server its own key there and records the station, with
// its confirmed host key, in the server's targets.json (docs/
// ARCHITECTURE.md section 7).
func TestStationAddAndAttach(t *testing.T) {
	s, e, home := addVPS(t, "")
	ctx := context.Background()
	nasHome := e.AddHost(config.StationAlias("nas"))
	st := config.Station{Name: "nas", Address: "198.51.100.7", User: "valise", Path: "/volume1/valise"}
	if _, err := s.StationScan(ctx, config.Station{Name: "Bad", Address: "x", User: "u", Path: "/p"}); err == nil {
		t.Fatal("bad name accepted")
	}
	keys, err := s.StationScan(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.StationAdd(ctx, st, keys); err != nil {
		t.Fatal(err)
	}
	ak, _ := os.ReadFile(filepath.Join(nasHome, ".ssh", "authorized_keys"))
	if !strings.HasPrefix(string(ak), "restrict ssh-ed25519 ") || !strings.Contains(string(ak), "valise-station@") {
		t.Fatalf("station authorized_keys:\n%s", ak)
	}
	if cfg, _ := os.ReadFile(filepath.Join(s.Dirs.Data, config.SSHConfigFile)); !strings.Contains(string(cfg), "Host valise-station-nas\n  HostName 198.51.100.7") {
		t.Fatalf("ssh_config:\n%s", cfg)
	}
	if err := s.StationAdd(ctx, st, keys); err == nil {
		t.Fatal("added twice")
	}

	// Attaching needs the owner: register the key, answer the passphrase.
	if _, err := s.KeyInit(pass); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.KeyRegister(ctx, "vps", "laptop"); err != nil {
		t.Fatal(err)
	}
	s.OwnerAuth.Passphrase = func(string) (string, error) { return pass, nil }
	if err := s.attachStation(ctx, "vps", st); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".config/valise/station-keys/nas")); err != nil {
		t.Fatal("server station key missing")
	}
	ak, _ = os.ReadFile(filepath.Join(nasHome, ".ssh", "authorized_keys"))
	if strings.Count(string(ak), "restrict ") != 2 {
		t.Fatalf("server key not on the station:\n%s", ak)
	}
	tj, _ := os.ReadFile(filepath.Join(home, ".config/valise/targets.json"))
	if !strings.Contains(string(tj), `"address": "198.51.100.7"`) || !strings.Contains(string(tj), "198.51.100.7 ssh-ed25519") {
		t.Fatalf("targets.json:\n%s", tj)
	}
	status, err := s.VaultStatus(ctx, "vps")
	if err != nil || !attached(status, st) {
		t.Fatalf("not attached: %v", err)
	}
	if err := s.StationRemove("nas"); err != nil || len(s.Stations()) != 0 {
		t.Fatal("not removed")
	}
}
