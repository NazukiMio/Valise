package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"valise/client/config"
)

// Adding a station installs the client's restricted key and the line
// trusting Valise's certificates; a server's own key only works with a
// certificate the client signs: limited to rrsync in the server's folder,
// for a while (docs/ARCHITECTURE.md section 7).
func TestStationCertificates(t *testing.T) {
	s, e, home := addVPS(t, "")
	ctx := context.Background()
	nasHome := e.AddHost(config.StationAlias("nas"))
	if _, err := s.KeyInit(pass); err != nil {
		t.Fatal(err)
	}
	st := config.Station{Name: "nas", Address: "198.51.100.7", User: "valise", Path: filepath.Join(nasHome, "valise"), AnySource: true}
	if _, err := s.StationScan(ctx, config.Station{Name: "Bad", Address: "x", User: "u", Path: "/p"}); err == nil {
		t.Fatal("bad name accepted")
	}
	keys, err := s.StationScan(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.StationAdd(ctx, st, keys)
	if err != nil {
		t.Fatal(err)
	}
	ak, _ := os.ReadFile(filepath.Join(nasHome, ".ssh", "authorized_keys"))
	if !strings.Contains(string(ak), "\nrestrict ssh-ed25519 ") && !strings.HasPrefix(string(ak), "restrict ssh-ed25519 ") ||
		!strings.Contains(string(ak), `cert-authority,restrict,principals="valise" ssh-ed25519 `) {
		t.Fatalf("station authorized_keys:\n%s", ak)
	}
	if res.Station.RRsync || len(res.Warnings) == 0 {
		t.Fatalf("the fake station has no rrsync: %+v", res)
	}
	if b, _ := os.ReadFile(s.caPath() + ".age"); len(b) == 0 || strings.Contains(string(b), "PRIVATE KEY") {
		t.Fatal("signing key not kept encrypted")
	}

	// Pretend the station has rrsync; attaching needs the owner.
	list := s.Stations()
	list[0].RRsync = true
	config.SaveStations(s.Dirs.Data, list)
	if _, _, err := s.KeyRegister(ctx, "vps", "laptop"); err != nil {
		t.Fatal(err)
	}
	s.OwnerAuth.Passphrase = func(string) (string, error) { return pass, nil }
	if err := s.ServerNameSet(ctx, "vps", "vps"); err != nil {
		t.Fatal(err)
	}
	status, err := s.VaultStatus(ctx, "vps")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.attachStation(ctx, "vps", list[0], status); err != nil {
		t.Fatal(err)
	}
	cert := filepath.Join(home, ".config/valise/station-keys/nas-cert.pub")
	out, err := exec.Command("ssh-keygen", "-L", "-f", cert).CombinedOutput()
	if err != nil {
		t.Fatalf("%v %s", err, out)
	}
	if _, err := os.Stat(filepath.Join(nasHome, "valise", "vps")); err != nil {
		t.Fatal("the server's folder was not made on the station")
	}
	for _, want := range []string{"force-command rrsync -wo -no-del -no-overwrite " + filepath.Join(nasHome, "valise", "vps"), "valise", "Valid: from"} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("certificate lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(string(out), "permit-pty") {
		t.Fatalf("certificate permits a terminal:\n%s", out)
	}
	ak, _ = os.ReadFile(filepath.Join(nasHome, ".ssh", "authorized_keys"))
	if strings.Contains(string(ak), "valise-station@vps") {
		t.Fatal("the server's own key must not be authorized on the station")
	}
	tj, _ := os.ReadFile(filepath.Join(home, ".config/valise/targets.json"))
	if !strings.Contains(string(tj), `"rooted": true`) || !strings.Contains(string(tj), `"kind": "rrsync"`) {
		t.Fatalf("targets.json:\n%s", tj)
	}
	status, _ = s.VaultStatus(ctx, "vps")
	if !attached(status, list[0]) || !s.certFresh("vps", "nas") {
		t.Fatal("not attached with a fresh certificate")
	}
	if err := s.StationRemove("nas"); err != nil || len(s.Stations()) != 0 {
		t.Fatal("not removed")
	}
}
