package ssh

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const sampleConfig = `# comment
Host ubuntu-vm
  HostName 10.211.55.5
  User parallels
  IdentityFile ~/.ssh/id_ed25519

Host ubuntu-vm-x86 nas-*
  HostName=%h.lan
  User = nazukimio
  Port 2222
  ProxyJump ubuntu-vm

Host "quoted host" !nas-test
  User quoted

Match host ubuntu-vm
  User ignored

Host *
  User fallback
  IdentityFile ~/.ssh/id_rsa
  ControlPath none
`

func TestLookup(t *testing.T) {
	c, err := Parse(strings.NewReader(sampleConfig), "/home/u")
	if err != nil {
		t.Fatal(err)
	}
	got := c.Lookup("ubuntu-vm")
	want := HostConfig{Alias: "ubuntu-vm", HostName: "10.211.55.5", User: "parallels",
		IdentityFiles: []string{"/home/u/.ssh/id_ed25519", "/home/u/.ssh/id_rsa"}, ControlPath: "none"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
	if got.UserControlMaster() {
		t.Fatal("ControlPath none is not connection sharing")
	}
	x86 := c.Lookup("ubuntu-vm-x86")
	if x86.HostName != "ubuntu-vm-x86.lan" || x86.User != "nazukimio" || x86.Port != 2222 || x86.ProxyJump != "ubuntu-vm" {
		t.Fatalf("%+v", x86)
	}
	// nas-test matches "nas-*" but is excluded from the quoted block.
	if nas := c.Lookup("nas-test"); nas.User != "nazukimio" || nas.HostName != "nas-test.lan" {
		t.Fatalf("%+v", nas)
	}
	if q := c.Lookup("quoted host"); q.User != "quoted" {
		t.Fatalf("%+v", q)
	}
	if other := c.Lookup("other"); other.HostName != "other" || other.User != "fallback" {
		t.Fatalf("%+v", other)
	}
	if hosts := c.Hosts(); !reflect.DeepEqual(hosts, []string{"ubuntu-vm", "ubuntu-vm-x86", "quoted host"}) {
		t.Fatalf("%v", hosts)
	}
}

func TestIncludeAndControlMaster(t *testing.T) {
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	os.MkdirAll(filepath.Join(sshDir, "conf.d"), 0o700)
	os.WriteFile(filepath.Join(sshDir, "conf.d", "a.conf"), []byte("Host inc\n  HostName 192.0.2.1\n  ControlMaster auto\n  ControlPath ~/.ssh/cm-%C\n"), 0o600)
	os.WriteFile(filepath.Join(sshDir, "config"), []byte("Host top\n  Include conf.d/*.conf\n  User topuser\n"), 0o600)
	c, err := ParseFile(filepath.Join(sshDir, "config"), home)
	if err != nil {
		t.Fatal(err)
	}
	inc := c.Lookup("inc")
	if inc.HostName != "192.0.2.1" || !inc.UserControlMaster() || inc.User != "" {
		t.Fatalf("%+v", inc)
	}
	if top := c.Lookup("top"); top.User != "topuser" {
		t.Fatalf("lines after Include must stay in the including block: %+v", top)
	}
	if _, err := ParseFile(filepath.Join(home, "missing"), home); err != nil {
		t.Fatal(err)
	}
}

func TestSplitLineErrors(t *testing.T) {
	if _, _, err := splitLine(`Host "unterminated`); err == nil {
		t.Fatal("accepted")
	}
}

func TestHosts(t *testing.T) {
	c, err := Parse(strings.NewReader("Host a b\n  HostName x\nHost *.lan !c\nHost a\nMatch host d\nHost e\n"), "/home/u")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(c.Hosts(), ","); got != "a,b,e" {
		t.Fatalf("%s", got)
	}
}
