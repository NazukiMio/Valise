package gui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/client/config"
	"valise/testbed/fakesshenv"
	"valise/ui/service"
)

const fixServerPassword = "server-pass-1"

// fixWorld is a server added by hand over fake-ssh with no admin key: a
// management login there needs the server's password. Once enforced, the
// ssh stand-in asks for it the way OpenSSH does, through SSH_ASKPASS, and
// refuses a login without the right answer. fake-ssh proves Valise's own
// path only; real OpenSSH and sshd need a target machine.
type fixWorld struct {
	a    *App
	tt   *ui.Tester
	mu   sync.Mutex
	home string
	asks string // the file counting password questions
}

const fixProfile = `schema: valise-profile/1
id: demo
display_name: Demo
profile_version: 1
status: draft
detect: { binaries: [demo], state_dir_candidates: ["${HOME}/.demo"], markers_any: [demo.json] }
paths: [{ root: state, glob: "demo.json", category: config, verify: pending }]
provenance: { kind: binary, binary: { name: demo } }
actions:
  - { id: retune, name: 重新配置, explain: "固定配置动作", undo: "恢复备份", risk: config, argv: [/bin/sh, -c, "echo retuned >> $HOME/ran"], output: none }
`

const fixLoginctl = `#!/bin/sh
case "$1" in
 show-user) cat "$HOME/linger" 2>/dev/null || echo no ;;
 enable-linger) echo "$*" >> "$HOME/ran"; printf yes > "$HOME/linger" ;;
esac
`

func newFixWorld(t *testing.T) *fixWorld {
	t.Helper()
	for _, tool := range []string{"ssh-keygen", "ssh-agent", "ssh-add"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not installed")
		}
	}
	e := fakesshenv.New(t)
	w := &fixWorld{home: e.AddHost("vps")}
	dir := t.TempDir()
	enforce := filepath.Join(dir, "enforce")
	w.asks = filepath.Join(dir, "asks")
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// The password login of OpenSSH: no key offered, so it asks.
	wrapper := filepath.Join(dir, "ssh")
	write(wrapper, `#!/bin/sh
case " $* " in *PubkeyAuthentication=no*)
 if test -e '`+enforce+`'; then
  echo ask >> '`+w.asks+`'
  test -n "$SSH_ASKPASS" || { echo 'Permission denied (password).' >&2; exit 255; }
  pw=$("$SSH_ASKPASS" "me@203.0.113.9's password: ") || { echo 'Permission denied (password).' >&2; exit 255; }
  test "$pw" = '`+fixServerPassword+`' || { echo 'Permission denied, please try again.' >&2; exit 255; }
 fi ;;
esac
exec '`+e.SSH+`' "$@"
`)
	s := &service.Service{Dirs: config.Dirs{Data: filepath.Join(dir, "data"), Cache: filepath.Join(dir, "cache")},
		Config: config.Config{SSHBinary: wrapper}, Version: "test", Home: dir, SSHConfigPath: filepath.Join(dir, "ssh_config")}
	t.Cleanup(s.Close)
	hk := filepath.Join(dir, "hk")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", hk).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	pub, _ := os.ReadFile(hk + ".pub")
	f := strings.Fields(string(pub))
	s.Keyscan = func(_ context.Context, host string, _ int) ([]byte, error) {
		return []byte(host + " " + f[0] + " " + f[1] + "\n"), nil
	}
	ctx := context.Background()
	srv := config.Server{Name: "vps", HostName: "203.0.113.9", Port: 22, User: "me"}
	keys, err := s.ScanHostKeys(ctx, srv)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.TrustHostKeys(ctx, srv, keys); err != nil {
		t.Fatal(err)
	}
	if err := s.AddServer(ctx, service.AddServerOptions{Server: srv, Setup: service.SetupOptions{Deploy: service.DeploySource{LocalPath: e.Porter()}}}); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(w.home, ".demo/demo.json"), "{}")
	write(filepath.Join(w.home, ".local/bin/demo"), "#!/bin/sh\necho demo 1.0\n")
	write(filepath.Join(w.home, ".local/bin/loginctl"), fixLoginctl)
	// A timer depends on linger; time sync is off too, whose fix the desktop
	// does not offer yet.
	write(filepath.Join(w.home, ".config/systemd/user/valise-seal@demo.timer"), "[Timer]\nOnUnitActiveSec=21600s\n")
	write(filepath.Join(w.home, ".local/bin/timedatectl"), "#!/bin/sh\nprintf 'CanNTP=yes\\nNTP=no\\nNTPSynchronized=no\\n'\n")
	write(filepath.Join(w.home, ".local/bin/systemctl"), "#!/bin/sh\nprintf 'LoadState=not-found\\n'\n")
	write(filepath.Join(s.Dirs.Data, "profiles/demo.yaml"), fixProfile)
	// Steps that must prove it is the owner use the Valise passphrase; that
	// is not the management login this world is about.
	const pass = "test-passphrase-1234"
	if _, err := s.KeyInit(pass); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.KeyRegister(ctx, "vps", "laptop"); err != nil {
		t.Fatal(err)
	}
	s.OwnerAuth.Passphrase = func(string) (string, error) { return pass, nil }
	if _, err := s.Enroll(ctx, "vps", service.EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatal(err)
	}
	if !s.Tiered("vps") || s.HasAdminKey("vps") {
		t.Fatal("the world needs a managed server without an admin key")
	}
	// From here on this is the desktop: no terminal, OpenSSH's question
	// comes to the window, and the server wants its password.
	w.a = New(s)
	s.NoTerminalLogin = true
	s.PasswordAuth.Prompt = w.a.ServerPassword
	write(enforce, "")
	w.a.update = func(f func()) { w.mu.Lock(); defer w.mu.Unlock(); f() }
	w.a.scanTimeline = func(context.Context, service.TimelineScanRequest, func(service.TimelineUpdate)) error { return nil }
	w.a.refreshAgents()
	w.tt = ui.NewTester(w.a.View, 1180, 900)
	w.wait(t, "the agent list", func() bool { return len(w.a.agents.value.Items) == 1 })
	w.do(func() {
		w.a.navigate("/agents/vps/demo")
		w.a.agentTab = tabHealth
	})
	return w
}

// do runs f as the main thread would: never while work applies a result.
func (w *fixWorld) do(f func()) {
	w.mu.Lock()
	defer w.mu.Unlock()
	f()
	w.tt.Frame()
	w.tt.Advance(time.Second)
}

func (w *fixWorld) wait(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for {
		var ok bool
		w.do(func() { ok = cond() })
		if ok {
			return
		}
		if time.Now().After(deadline) {
			w.mu.Lock()
			defer w.mu.Unlock()
			t.Fatalf("timed out waiting for %s: %q", what, w.tt.Texts())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (w *fixWorld) click(t *testing.T, label string) {
	t.Helper()
	w.do(func() {
		if err := w.tt.Click(label); err != nil {
			t.Fatalf("%v in %q", err, w.tt.Texts())
		}
	})
}

// answer waits for the window's password dialog and answers it the way a
// person does; "" cancels. It returns once done holds.
func (w *fixWorld) answer(t *testing.T, password string, done func() bool) (asked int) {
	return w.answerWith(t, func(string) string { return password }, done)
}

func (w *fixWorld) answerWith(t *testing.T, respond func(prompt string) string, done func() bool) (asked int) {
	t.Helper()
	w.wait(t, "the fix to end", func() bool {
		if len(w.a.asks) == 0 {
			return done()
		}
		w.tt.Frame()
		p := w.a.asks[0]
		if !w.tt.HasText(T("ask.server.title", "who", "me@203.0.113.9")) &&
			!w.tt.HasText(T("ask.sudo.title", "who", "vps")) &&
			!w.tt.HasText(T("ask.sudo.generic")) {
			t.Fatalf("OpenSSH/sudo question is not in the window: %q", w.tt.Texts())
		}
		asked++
		pw := respond(p.host)
		if pw == "" {
			if err := w.tt.Click(T("common.cancel")); err != nil {
				t.Fatal(err)
			}
			return false
		}
		w.tt.Type(pw)
		if err := w.tt.Click(T("common.ok")); err != nil {
			t.Fatal(err)
		}
		return false
	})
	return asked
}

func (w *fixWorld) ran() string {
	b, _ := os.ReadFile(filepath.Join(w.home, "ran"))
	return string(b)
}

func (w *fixWorld) noTerminalAdvice(t *testing.T) {
	t.Helper()
	w.do(func() {
		for _, text := range w.tt.Texts() {
			if strings.Contains(text, "valise action") || strings.Contains(text, "valise health fix") {
				t.Fatalf("the window sends the person to a terminal: %q", text)
			}
		}
	})
}

// Without an admin key, the health page's linger fix lists its command and
// change first, runs only after the confirmation, logs in with the server
// password asked in the window, and tells the exit code and the re-check
// apart. A cancelled password changes nothing and can be retried.
func TestHealthLingerFixAsksServerPasswordInWindow(t *testing.T) {
	w := newFixWorld(t)
	st := func() *healthState { return w.a.health["vps/demo"] }
	const name = "开启 linger"
	w.wait(t, "the linger finding", func() bool { return w.tt.HasText(name) })
	w.do(func() {
		if !w.tt.HasText("服务器时钟") || slices.Contains(w.tt.Texts(), "开启时间同步") {
			t.Fatalf("the clock finding shows, its fix is not in the desktop yet: %q", w.tt.Texts())
		}
	})
	w.click(t, name)
	w.wait(t, "the confirmation", func() bool { return st().sys != nil && st().sys.confirm })
	w.do(func() {
		f := st().sys.fix
		cmd := strings.Join(f.Steps[0].Command, " ")
		if f.Mode != service.FixDirect || !strings.HasPrefix(cmd, "loginctl enable-linger ") {
			t.Fatalf("plan %+v", f)
		}
		for _, want := range []string{cmd, T("health.sysfix.change", "what", f.Steps[0].Change), T("health.sysfix.undo", "how", f.Steps[0].Undo),
			T("health.sysfix.cmds", "server", "vps"), f.Goal, f.Impact, f.Access} {
			if !w.tt.HasText(want) {
				t.Errorf("the confirmation does not show %q: %q", want, w.tt.Texts())
			}
		}
		if !strings.Contains(f.Access, "服务器密码") {
			t.Errorf("the confirmation does not say which login it needs: %q", f.Access)
		}
	})
	shotLocked(t, w, "health-linger-confirm")
	if w.ran() != "" {
		t.Fatal("planning or showing the confirmation ran a command")
	}
	// Cancelling the password runs nothing and stays in the window.
	w.click(t, T("health.run"))
	if n := w.answer(t, "", func() bool { return !st().sys.busy() }); n == 0 {
		t.Fatal("the server password was not asked for")
	}
	if st().sys.err == nil || w.ran() != "" {
		t.Fatalf("a cancelled login: err %v, ran %q", st().sys.err, w.ran())
	}
	w.noTerminalAdvice(t)
	// Retry, confirm again, and answer.
	w.click(t, T("common.retry"))
	w.wait(t, "the confirmation again", func() bool { return st().sys != nil && st().sys.confirm })
	w.click(t, T("health.run"))
	if n := w.answer(t, fixServerPassword, func() bool { return !st().sys.busy() }); n == 0 {
		t.Fatal("the server password was not asked for")
	}
	w.do(func() {
		f := st().sys
		if f.err != nil || f.result == nil {
			t.Fatalf("fix failed: %v", f.err)
		}
		r := f.result
		if len(r.Steps) != 1 || !r.Steps[0].Ran || r.Steps[0].Exit != 0 || r.Verification == nil || r.Verification.State != "resolved" || !r.Audited {
			t.Fatalf("result %+v %+v", r, r.Verification)
		}
		cmd := strings.Join(r.Steps[0].Command, " ")
		if !w.tt.HasText(T("health.sysfix.exit", "cmd", cmd, "code", 0)) || !w.tt.HasText(T("health.sysfix.recheck", "text", r.Verification.Text)) {
			t.Fatalf("exit code and re-check are not shown apart: %q", w.tt.Texts())
		}
		if w.tt.HasText(T("common.retry")) {
			t.Fatal("a resolved fix offers a retry")
		}
	})
	if !strings.Contains(w.ran(), "enable-linger ") {
		t.Fatalf("ran %q", w.ran())
	}
	w.noTerminalAdvice(t)
	shotLocked(t, w, "health-linger-result")
	w.wait(t, "health read again", func() bool { return !st().busy && !w.tt.HasText(name) })
}

// A repair action that needs a management login runs from the health page
// the same way: confirmation, the server password in the window, the
// result. Nothing tells the person to run `valise action` in a terminal.
func TestHealthActionAsksServerPasswordInWindow(t *testing.T) {
	w := newFixWorld(t)
	w.a.svc.RiskAccepted = true // the escape-pod gate is not what this test is about
	st := func() *healthState { return w.a.health["vps/demo"] }
	w.wait(t, "health", func() bool { return st() != nil && st().report != nil })
	w.do(func() {
		row := &agentRow{Server: "vps", Instance: "demo"}
		w.a.prepareFix(row, st(), service.HealthItem{ID: "service", ActionID: "retune", ActionName: "重新配置"})
	})
	w.wait(t, "the confirmation", func() bool { return st().fix != nil && (st().fix.confirm || st().fix.err != nil) })
	w.do(func() {
		if st().fix.err != nil {
			t.Fatal(st().fix.err)
		}
		if !w.tt.HasText(T("health.admin_login")) || !w.tt.HasText(T("health.run_cmd", "server", "vps")) {
			t.Fatalf("confirmation: %q", w.tt.Texts())
		}
		// The backup before a config action is its own subject; run the
		// action alone here.
		st().fix.action.Risk = "manage"
	})
	if w.ran() != "" {
		t.Fatal("the confirmation ran the action")
	}
	w.click(t, T("health.run"))
	n := w.answer(t, fixServerPassword, func() bool { return st().fix != nil && !st().fix.running })
	w.do(func() {
		f := st().fix
		if f.err != nil || f.result == nil || f.result.Exit != 0 {
			t.Fatalf("action: %v %+v", f.err, f.result)
		}
		if !w.tt.HasText(T("health.exited", "code", 0)) {
			t.Fatalf("no exit code: %q", w.tt.Texts())
		}
	})
	if n == 0 || !strings.Contains(w.ran(), "retuned") {
		t.Fatalf("asked %d times, ran %q", n, w.ran())
	}
	w.noTerminalAdvice(t)
}

func shotLocked(t *testing.T, w *fixWorld, name string) {
	t.Helper()
	w.mu.Lock()
	defer w.mu.Unlock()
	shot(t, w.tt, name)
}
