package login

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestEntryAuth(t *testing.T) {
	home := t.TempDir()
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "demo"), []byte(`#!/bin/sh
case "$1" in
 url) echo 'Dashboard: http://127.0.0.1:9000/ui/?x=1#token=abc%2Fd';;
 tok) printf '\n s3cr+t \n';;
 wrongport) echo http://127.0.0.1:1/;;
 json) echo '{"url":"http://127.0.0.1:9000/#token=long","a":{"b":"http://127.0.0.1:9000/#once=1"}}';;
 fail) echo leaked-secret >&2; exit 3;;
esac
`), 0o755)
	e := &porterenroll.Env{Home: home, Vars: []string{"PATH=" + bin + ":/usr/bin:/bin"}}
	porterenroll.WriteRecord(home, enroll.Record{InstanceID: "demo", ProfileID: "demo", ProfileVersion: 1, Roots: map[string]string{"state": home}}, profile.Profile{ID: "demo", ProfileVersion: 1})
	entry := func(a profile.EntryAuth) login.EntryAuthParams {
		return login.EntryAuthParams{InstanceID: "demo", Entry: profile.Entry{ID: "m", Port: 9000, Path: "/ui/", Auth: &a}}
	}
	ctx := context.Background()
	r, err := EntryAuth(ctx, e, home, entry(profile.EntryAuth{URLArgv: profile.Argv{"demo", "url"}}))
	if err != nil || r.Suffix != "/ui/?x=1#token=abc%2Fd" {
		t.Fatalf("%+v %v", r, err)
	}
	r, err = EntryAuth(ctx, e, home, entry(profile.EntryAuth{TokenArgv: profile.Argv{"demo", "tok"}, URLSuffix: "#token=${TOKEN}"}))
	if err != nil || r.Suffix != "/ui/#token=s3cr%2Bt" {
		t.Fatalf("%+v %v", r, err)
	}
	r, err = EntryAuth(ctx, e, home, entry(profile.EntryAuth{URLArgv: profile.Argv{"demo", "json"}, URLField: "a.b"}))
	if err != nil || r.Suffix != "/#once=1" {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := EntryAuth(ctx, e, home, entry(profile.EntryAuth{URLArgv: profile.Argv{"demo", "json"}, URLField: "missing"})); err == nil {
		t.Fatal("missing field accepted")
	}
	if _, err := EntryAuth(ctx, e, home, entry(profile.EntryAuth{URLArgv: profile.Argv{"demo", "wrongport"}})); err == nil {
		t.Fatal("URL for another port accepted")
	}
	_, err = EntryAuth(ctx, e, home, entry(profile.EntryAuth{URLArgv: profile.Argv{"demo", "fail"}}))
	if err == nil || strings.Contains(fmt.Sprint(err), "leaked") {
		t.Fatalf("command output leaked into the error: %v", err)
	}
}

func TestEntryAuthURLVariantsAndLingeringProcess(t *testing.T) {
	oldGrace, oldPoll := entryAuthLingeringGrace, entryAuthPoll
	entryAuthLingeringGrace, entryAuthPoll = 120*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() {
		entryAuthLingeringGrace, entryAuthPoll = oldGrace, oldPoll
	})

	home := t.TempDir()
	bin := t.TempDir()
	script := `#!/bin/sh
case "$1" in
 ansi)
   printf '\033[36mhttp://127.0.0.1:9120/#ticket=ansi-ticket-1\033[0m)\n'
   ;;
 bare-then-tok)
   echo 'Listening on http://127.0.0.1:9120/'
   echo 'Sign in: http://127.0.0.1:9120/auth#ticket=one-use-2'
   ;;
 stderr-only)
   echo 'http://127.0.0.1:9120/?ticket=stderr-tok-3' >&2
   ;;
 linger)
   echo 'Open http://127.0.0.1:9120/#ticket=linger-tok-4'
   sleep 30
   ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "demo"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	e := &porterenroll.Env{Home: home, Vars: []string{"PATH=" + bin + ":/usr/bin:/bin"}}
	if _, err := porterenroll.WriteRecord(home, enroll.Record{InstanceID: "demo", ProfileID: "demo", ProfileVersion: 1, Roots: map[string]string{"state": home}}, profile.Profile{ID: "demo", ProfileVersion: 1}); err != nil {
		t.Fatal(err)
	}
	entry := func(arg string) login.EntryAuthParams {
		return login.EntryAuthParams{InstanceID: "demo", Entry: profile.Entry{ID: "webapp", Port: 9120, Path: "/", Auth: &profile.EntryAuth{URLArgv: profile.Argv{"demo", arg}}}}
	}
	ctx := context.Background()

	for _, tc := range []struct{ arg, want string }{
		{"ansi", "/#ticket=ansi-ticket-1"},
		{"bare-then-tok", "/auth#ticket=one-use-2"},
		{"stderr-only", "/?ticket=stderr-tok-3"},
	} {
		res, err := EntryAuth(ctx, e, home, entry(tc.arg))
		if err != nil || res.Suffix != tc.want {
			t.Fatalf("%s: got %+v err=%v, want %q", tc.arg, res, err, tc.want)
		}
	}

	start := time.Now()
	res, err := EntryAuth(ctx, e, home, entry("linger"))
	if err != nil || res.Suffix != "/#ticket=linger-tok-4" {
		t.Fatalf("linger: got %+v err=%v", res, err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("linger took %v, expected prompt termination after grace", d)
	}
}

func TestLoopbackListeners(t *testing.T) {
	dir := t.TempDir()
	ProcNet = dir
	loopbackUID = func() int { return 1000 }
	t.Cleanup(func() { ProcNet = "/proc/net"; loopbackUID = os.Getuid })
	tcp := `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:23A0 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 1 1 0 100 0 0 10 0
   1: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 2 1 0 100 0 0 10 0
   2: 00000000:0050 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 3 1 0 100 0 0 10 0
   3: 0100007F:0051 0100007F:D431 01 00000000:00000000 00:00000000 00000000  1000        0 4 1 0 100 0 0 10 0
   4: 0100007F:23A0 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 5 1 0 100 0 0 10 0
   5: 0100007F:270F 00000000:0000 0A 00000000:00000000 00:00000000 00000000   bad        0 8 1 0 100 0 0 10 0
   6: 0100007F:2710 00000000:0000 0A 00000000:00000000
`
	tcp6 := `  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000000000000000000001000000:0BB8 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 6 1 0 100 0 0 10 0
   1: 00000000000000000000000000000000:1F91 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 7 1 0 100 0 0 10 0
`
	os.WriteFile(filepath.Join(dir, "tcp"), []byte(tcp), 0o644)
	os.WriteFile(filepath.Join(dir, "tcp6"), []byte(tcp6), 0o644)
	res, err := LoopbackListeners()
	if err != nil || len(res.Ports) != 2 || res.Ports[0] != 3000 || res.Ports[1] != 9120 {
		t.Fatalf("%+v %v", res, err)
	}
	ProcNet = t.TempDir()
	if res, _ := LoopbackListeners(); res.Ports == nil || len(res.Ports) != 0 {
		t.Fatalf("%+v", res)
	}
}
