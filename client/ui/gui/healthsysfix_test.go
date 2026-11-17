package gui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// Non-sudo linger fails with exit 1 -> result card directly offers
// "使用 sudo 重试" -> confirmation lists `sudo -S loginctl enable-linger`
// -> user answers server & sudo passwords -> resolved and health refreshes.
func TestHealthLingerSudoRetryInWindow(t *testing.T) {
	w := newFixWorld(t)
	bin := filepath.Join(w.home, ".local/bin")
	if err := os.WriteFile(filepath.Join(w.home, "deny"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.home, "sudo-need-pw"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "loginctl"), []byte(`#!/bin/sh
case "$1" in
 show-user) cat "$HOME/linger" 2>/dev/null || echo no ;;
 enable-linger)
  echo "$*" >> "$HOME/ran"
  if test -e "$HOME/deny" && test -z "$SUDO_OK"; then echo 'Could not enable linger: Access denied' >&2; exit 1; fi
  printf yes > "$HOME/linger" ;;
esac
`), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "sudo"), []byte(`#!/bin/sh
if [ "$1" = "-n" ] && [ "$2" = "true" ]; then
 if test -e "$HOME/sudo-need-pw"; then exit 1; fi
 exit 0
fi
if [ "$1" = "-S" ]; then
 shift
 if test -e "$HOME/sudo-need-pw"; then
  IFS= read -r pw || exit 1
  if [ "$pw" != "sudo-secret-7" ]; then echo 'Sorry, try again.' >&2; exit 1; fi
 fi
fi
echo sudo >> "$HOME/ran"
export SUDO_OK=1
exec "$@"
`), 0o755); err != nil {
		t.Fatal(err)
	}

	st := func() *healthState { return w.a.health["vps/demo"] }
	const name = "开启 linger"
	w.wait(t, "the linger finding", func() bool { return w.tt.HasText(name) })
	w.click(t, name)
	w.wait(t, "the confirmation", func() bool { return st().sys != nil && st().sys.confirm })
	w.click(t, T("health.run"))
	if n := w.answer(t, fixServerPassword, func() bool { return !st().sys.busy() }); n == 0 {
		t.Fatal("server password was not asked")
	}
	w.do(func() {
		f := st().sys
		if f.result == nil || f.result.Steps[0].Exit != 1 || f.result.Verification.State != "remaining" {
			t.Fatalf("expected non-sudo failure: %+v", f.result)
		}
		if !w.tt.HasText(T("health.sysfix.retry_sudo")) {
			t.Fatalf("missing retry-sudo button: %q", w.tt.Texts())
		}
	})

	// Click "使用 sudo 重试" on the result card.
	w.click(t, T("health.sysfix.retry_sudo"))
	w.wait(t, "the sudo confirmation", func() bool { return st().sys != nil && st().sys.confirm })
	w.do(func() {
		f := st().sys.fix
		cmd := strings.Join(f.Steps[0].Command, " ")
		if !strings.HasPrefix(cmd, "sudo -S loginctl enable-linger ") {
			t.Fatalf("unexpected sudo step: %q", cmd)
		}
		if !w.tt.HasText(cmd) || !w.tt.HasText(T("health.sysfix.sudo_note")) {
			t.Fatalf("sudo confirmation missing command or prompt note: %q", w.tt.Texts())
		}
	})
	w.click(t, T("health.run"))
	var seenSudo bool
	w.answerWith(t, func(prompt string) string {
		if strings.Contains(strings.ToLower(prompt), "sudo") {
			seenSudo = true
			return "sudo-secret-7"
		}
		return fixServerPassword
	}, func() bool { return !st().sys.busy() })
	if !seenSudo {
		t.Fatal("sudo password was not asked")
	}
	w.do(func() {
		f := st().sys
		if f.err != nil || f.result == nil || f.result.Steps[0].Exit != 0 || f.result.Verification.State != "resolved" || !f.result.Audited {
			t.Fatalf("sudo fix failed: err=%v res=%+v", f.err, f.result)
		}
		if w.tt.HasText(T("health.sysfix.retry_sudo")) {
			t.Fatal("resolved fix still shows sudo retry")
		}
	})
	w.wait(t, "health cleared", func() bool { return !st().busy && !w.tt.HasText(name) })
}

func TestServerPromptMetaKeyPassphraseAndSudo(t *testing.T) {
	title, why, field, _ := serverPromptMeta("Enter passphrase for key '/tmp/id_ed25519':")
	if title != T("ask.key_passphrase") || field != T("ask.key_passphrase_field") ||
		!strings.Contains(title, "SSH 管理连接") || !strings.Contains(why, "非 sudo") {
		t.Fatalf("key passphrase meta: %q %q %q", title, why, field)
	}
	title, why, _, _ = serverPromptMeta("parallels@debian-vm's password:")
	if !strings.Contains(title, "SSH 管理连接") || !strings.Contains(why, "非 sudo") {
		t.Fatalf("server password meta: %q %q", title, why)
	}
	title, why, field, _ = serverPromptMeta("[sudo] password for vps:")
	if title != T("ask.sudo.title", "who", "vps") || field != T("ask.sudo.field") ||
		!strings.Contains(title, "sudo 提权密码") || !strings.Contains(why, "非 SSH") {
		t.Fatalf("sudo meta: %q %q %q", title, why, field)
	}
}

func TestPassDialogEmptyAllowedOnlyForAskServer(t *testing.T) {
	a, tt := world(t)
	// askServer allows empty submit with OK button and Enter key.
	r1 := make(chan passReply, 1)
	a.asks = []*passPrompt{{host: "vps's password:", kind: askServer, open: true, reply: r1}}
	tt.Frame()
	click(t, tt, T("common.ok"))
	select {
	case got := <-r1:
		if got.err != nil || got.pass != "" {
			t.Fatalf("empty OK on askServer: %+v", got)
		}
	default:
		t.Fatal("askServer blocked empty OK")
	}

	r2 := make(chan passReply, 1)
	a.asks = []*passPrompt{{host: "[sudo] password for vps:", kind: askServer, open: true, reply: r2}}
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	select {
	case got := <-r2:
		if got.err != nil || got.pass != "" {
			t.Fatalf("empty Enter on askServer: %+v", got)
		}
	default:
		t.Fatal("askServer blocked empty Enter")
	}

	// askAdmin and askOwner still reject empty submit.
	for _, kind := range []int{askAdmin, askOwner} {
		ch := make(chan passReply, 1)
		a.asks = []*passPrompt{{host: "vps", kind: kind, open: true, reply: ch}}
		tt.Frame()
		tt.Key(0, ui.KeyEnter)
		click(t, tt, T("common.ok"))
		select {
		case got := <-ch:
			t.Fatalf("kind %v unexpectedly submitted empty input: %+v", kind, got)
		default:
		}
		a.asks = nil
	}
}

// NOPASSWD server + ServerPassword empty submit: `sudo -n true` passes so
// GUI never opens a sudo password prompt, while OpenSSH prompt allows empty.
func TestHealthLingerNopasswdSudoRetryInWindow(t *testing.T) {
	w := newFixWorld(t)
	bin := filepath.Join(w.home, ".local/bin")
	if err := os.WriteFile(filepath.Join(w.home, "deny"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "loginctl"), []byte(`#!/bin/sh
case "$1" in
 show-user) cat "$HOME/linger" 2>/dev/null || echo no ;;
 enable-linger)
  echo "$*" >> "$HOME/ran"
  if test -e "$HOME/deny" && test -z "$SUDO_OK"; then echo 'Could not enable linger: Access denied' >&2; exit 1; fi
  printf yes > "$HOME/linger" ;;
esac
`), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "sudo"), []byte(`#!/bin/sh
if [ "$1" = "-n" ] && [ "$2" = "true" ]; then
 exit 0
fi
if [ "$1" = "-S" ]; then
 shift
fi
echo sudo >> "$HOME/ran"
export SUDO_OK=1
exec "$@"
`), 0o755); err != nil {
		t.Fatal(err)
	}

	st := func() *healthState { return w.a.health["vps/demo"] }
	const name = "开启 linger"
	w.wait(t, "the linger finding", func() bool { return w.tt.HasText(name) })
	w.click(t, name)
	w.wait(t, "confirmation", func() bool { return st().sys != nil && st().sys.confirm })
	w.click(t, T("health.run"))
	w.answer(t, fixServerPassword, func() bool { return !st().sys.busy() })
	w.click(t, T("health.sysfix.retry_sudo"))
	w.wait(t, "sudo confirmation", func() bool { return st().sys != nil && st().sys.confirm })
	w.click(t, T("health.run"))
	var seenSudo bool
	w.answerWith(t, func(prompt string) string {
		if strings.Contains(strings.ToLower(prompt), "sudo") {
			seenSudo = true
		}
		return fixServerPassword
	}, func() bool { return !st().sys.busy() })
	if seenSudo {
		t.Fatal("sudo prompt opened despite NOPASSWD sudo")
	}
	w.do(func() {
		f := st().sys
		if f.err != nil || f.result == nil || f.result.Steps[0].Exit != 0 || f.result.Verification.State != "resolved" {
			t.Fatalf("NOPASSWD linger fix failed: err=%v res=%+v", f.err, f.result)
		}
	})
}
