package login

import (
	"os"
	"path/filepath"
	"testing"

	"valise/core/enroll"
	"valise/core/login"
	"valise/core/profile"
	porterenroll "valise/porter/internal/enroll"
)

func TestSessionLifecycle(t *testing.T) {
	home, bin := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(bin, "demo"), []byte("#!/bin/sh\n"), 0o755)
	e := &porterenroll.Env{Home: home, Vars: []string{"PATH=" + bin, "BROWSER=firefox"}}
	rec := enroll.Record{InstanceID: "demo", ProfileID: "demo", ProfileVersion: 1, Roots: map[string]string{"state": "/s"}}
	if _, err := porterenroll.WriteRecord(home, rec, profile.Profile{ID: "demo", ProfileVersion: 1}); err != nil {
		t.Fatal(err)
	}
	l := profile.Login{ID: "p", Kind: login.KindLoopbackOAuth, Argv: profile.Argv{"demo", "auth", "--dir", "${STATE_DIR}"}}
	res, err := Prepare(e, login.PrepareParams{InstanceID: "demo", Login: l})
	if err != nil || !login.ValidSession(res.Session) || res.Argv[0] != filepath.Join(bin, "demo") || res.Argv[3] != "/s" {
		t.Fatalf("%+v %v", res, err)
	}
	if st, _ := os.Stat(sessionPath(home, res.Session)); st.Mode().Perm() != 0o600 {
		t.Fatalf("session mode %v", st.Mode())
	}
	if err := Shim(home, res.Session, []string{"https://auth.example/x?redirect_uri=http://localhost:1455/cb"}); err != nil {
		t.Fatal(err)
	}
	urls, err := URLs(home, res.Session)
	if err != nil || len(urls.URLs) != 1 {
		t.Fatalf("%+v %v", urls, err)
	}
	if _, err := End(home, res.Session); err != nil {
		t.Fatal(err)
	}
	if _, err := URLs(home, res.Session); err == nil {
		t.Fatal("ended session still readable")
	}
	// A stray shim call (no session) must not fail.
	if err := Shim(home, "", []string{"https://x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare(e, login.PrepareParams{InstanceID: "nope", Login: l}); err == nil {
		t.Fatal("unenrolled instance accepted")
	}
	if _, err := load(home, "../../etc/passwd"); err == nil {
		t.Fatal("path traversal in session id accepted")
	}
}

func TestListenAddrs(t *testing.T) {
	dir := t.TempDir()
	ProcNet = dir
	t.Cleanup(func() { ProcNet = "/proc/net" })
	tcp := `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:05AF 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 1 1 0000000000000000 100 0 0 10 0
   1: 0100007F:4965 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 2 1 0000000000000000 100 0 0 10 0
   2: 0100007F:05AF 0100007F:D431 01 00000000:00000000 00:00000000 00000000  1000        0 3 1 0000000000000000 100 0 0 10 0
`
	tcp6 := `  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000000000000000000001000000:05AF 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 4 1 0000000000000000 100 0 0 10 0
`
	os.WriteFile(filepath.Join(dir, "tcp"), []byte(tcp), 0o644)
	os.WriteFile(filepath.Join(dir, "tcp6"), []byte(tcp6), 0o644)
	res, err := ListenAddrs(1455)
	if err != nil || len(res.Addrs) != 2 || res.Addrs[0] != "127.0.0.1" || res.Addrs[1] != "::1" {
		t.Fatalf("%+v %v", res, err)
	}
	if res, _ := ListenAddrs(18789); len(res.Addrs) != 1 {
		t.Fatalf("%+v", res)
	}
	if res, _ := ListenAddrs(9); len(res.Addrs) != 0 {
		t.Fatalf("%+v", res)
	}
}
