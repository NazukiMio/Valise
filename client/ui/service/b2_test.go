package service

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"valise/client/config"
	"valise/client/keystore"
)

func TestB2AdminWindow(t *testing.T) {
	s := newService(t, "ssh")
	if s.AdminWindow() != 30*time.Minute {
		t.Fatal(s.AdminWindow())
	}
	for _, minutes := range []int{1, 30, 120, 240} {
		if err := s.SetAdminWindow(minutes); err != nil {
			t.Fatal(err)
		}
		cfg, err := config.Load(s.Dirs.Data)
		if err != nil || cfg.AdminWindowMinutes != minutes || s.AdminWindow() != time.Duration(minutes)*time.Minute {
			t.Fatalf("saved=%d window=%v err=%v", cfg.AdminWindowMinutes, s.AdminWindow(), err)
		}
	}
	for _, minutes := range []int{0, 241} {
		wantCode(t, s.SetAdminWindow(minutes), "admin_window_range")
		if s.AdminWindow() != 240*time.Minute {
			t.Fatal("invalid request changed the window")
		}
	}
}

func TestB2LinkedUnlockAndLegacyFallback(t *testing.T) {
	ctx := context.Background()
	s, _, _ := addVPS(t, pass)
	if _, err := s.KeyInit(pass); err != nil {
		t.Fatal(err)
	}
	const legacyPass = "an old independent passphrase"
	if err := s.createAdminKey(ctx, "legacy", legacyPass); err != nil {
		t.Fatal(err)
	}
	ownerAsked, adminAsked := 0, 0
	s.OwnerAuth.Passphrase = func(string) (string, error) { ownerAsked++; return pass, nil }
	s.AdminAuth.Passphrase = func(host string) (string, error) {
		adminAsked++
		if host != "legacy" {
			t.Fatalf("unexpected separate admin prompt for %s", host)
		}
		return legacyPass, nil
	}
	s.LockAdmin()
	if _, err := s.ownerIdentity("vps"); err != nil {
		t.Fatal(err)
	}
	if !s.AdminUnlocked(ctx, "vps") || s.AdminUnlocked(ctx, "legacy") {
		t.Fatal("linked unlock did not distinguish the keys")
	}
	if _, err := s.unlockAdmin(ctx, "vps"); err != nil || ownerAsked != 1 || adminAsked != 0 {
		t.Fatalf("linked unlock: owner=%d admin=%d %v", ownerAsked, adminAsked, err)
	}
	if _, err := s.unlockAdmin(ctx, "legacy"); err != nil || ownerAsked != 1 || adminAsked != 1 {
		t.Fatalf("legacy unlock: owner=%d admin=%d %v", ownerAsked, adminAsked, err)
	}
	s.LockAdmin()
	if _, err := s.unlockAdmin(ctx, "vps"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ownerIdentity("vps"); err != nil || ownerAsked != 2 || adminAsked != 1 {
		t.Fatalf("admin-first unlock: owner=%d admin=%d %v", ownerAsked, adminAsked, err)
	}
}

func TestB2PassphraseRotation(t *testing.T) {
	needOpenSSH(t)
	s := newService(t, "ssh")
	ctx := context.Background()
	const legacy = "an independent passphrase"
	const next = "the next Valise passphrase"
	if _, err := s.KeyInit(pass); err != nil {
		t.Fatal(err)
	}
	before, err := s.KeyShow()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnableRecovery(pass); err != nil {
		t.Fatal(err)
	}
	for host, phrase := range map[string]string{"unified": pass, "legacy": legacy} {
		if err := s.createAdminKey(ctx, host, phrase); err != nil {
			t.Fatal(err)
		}
	}
	legacyKey := readFile(t, s.keyPath("legacy", adminKeyName))
	if err := s.EnablePIN(pass, "012345"); err != nil {
		t.Fatal(err)
	}
	if err := s.ChangePassphrase(pass, next); err != nil {
		t.Fatal(err)
	}
	if after, err := s.KeyShow(); err != nil || after.Recipient != before.Recipient {
		t.Fatalf("recipient changed: %+v %v", after, err)
	}
	if got, err := s.VerifyPIN("012345"); err != nil || got != next {
		t.Fatalf("PIN did not follow rotation: %v", err)
	}
	for _, path := range []string{s.keystorePath(), filepath.Join(s.recoveryDir(), "r.age")} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		k, err := keystore.Unlock(data, next)
		if err != nil {
			t.Fatal(err)
		}
		k.Lock()
	}
	wantCode(t, s.loadAdmin(ctx, "unified", pass), "admin_passphrase_wrong")
	if err := s.loadAdmin(ctx, "unified", next); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, s.keyPath("legacy", adminKeyName)); got != legacyKey {
		t.Fatal("legacy key was rewritten")
	}
	if err := s.loadAdmin(ctx, "legacy", legacy); err != nil {
		t.Fatal(err)
	}
}

func TestB2PINPromptsAndDestroysWrapper(t *testing.T) {
	needOpenSSH(t)
	s := newService(t, "ssh")
	ctx := context.Background()
	if _, err := s.KeyInit(pass); err != nil {
		t.Fatal(err)
	}
	if err := s.createAdminKey(ctx, "h", pass); err != nil {
		t.Fatal(err)
	}
	wantCode(t, s.EnablePIN(pass, "１２３４５６"), "pin_format")
	wantCode(t, s.EnablePIN("wrong passphrase", "012345"), "passphrase_wrong")
	if err := s.EnablePIN(pass, "012345"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{s.pinPath(), s.pinStatePath()} {
		fi, err := os.Stat(path)
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("PIN file permissions: %v %v", fi, err)
		}
		sealed := readFile(t, path)
		if strings.Contains(sealed, pass) || strings.Contains(sealed, "012345") {
			t.Fatal("PIN or passphrase persisted in clear")
		}
	}
	pin, pinAsked, fullAsked := "012345", 0, 0
	s.OwnerAuth.PIN = func(string) (string, error) { pinAsked++; return pin, nil }
	s.OwnerAuth.Passphrase = func(string) (string, error) { fullAsked++; return pass, nil }
	_, err := s.VerifyPIN("654321")
	wantCode(t, err, "pin_wrong")
	for range 2 {
		if _, err := s.ownerIdentity("h"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.unlockAdmin(ctx, "h"); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := s.OwnerRemaining(); ok || s.AdminUnlocked(ctx, "h") || pinAsked != 4 || fullAsked != 0 {
		t.Fatalf("PIN mode cached credentials or missed prompts: PIN=%d full=%d", pinAsked, fullAsked)
	}
	if _, ok := s.AdminRemaining("h"); ok {
		t.Fatal("short PIN handshake displayed as an unlocked window")
	}
	pin = "654321"
	for i := 1; i <= 5; i++ {
		// Reopening the local files in a separate Service must preserve attempts.
		reopened := &Service{Dirs: s.Dirs, OwnerAuth: s.OwnerAuth}
		_, err := reopened.ownerIdentity("h")
		if i < 5 {
			wantCode(t, err, "pin_wrong")
			p, err := s.readPIN()
			if err != nil || p.Failures != i {
				t.Fatalf("stored failure count at attempt %d: %+v %v", i, p, err)
			}
		} else {
			wantCode(t, err, "pin_destroyed")
		}
	}
	if s.PINEnabled() || fileExists(s.pinStatePath()) || s.AdminUnlocked(ctx, "h") {
		t.Fatal("PIN files or reusable authorization survived the fifth failure")
	}
	if _, err := s.ownerIdentity("h"); err != nil || fullAsked != 1 {
		t.Fatalf("fallback: full=%d %v", fullAsked, err)
	}
}

func TestB2OwnerCacheCleared(t *testing.T) {
	s := newService(t, "ssh")
	if _, err := s.KeyInit(pass); err != nil {
		t.Fatal(err)
	}
	for _, clear := range []func(){s.ForgetOwner, s.LockAdmin, func() {
		s.owner.mu.Lock()
		s.owner.until = time.Now().Add(-time.Second)
		s.owner.mu.Unlock()
		s.OwnerRemaining()
	}, s.Close} {
		k, err := s.Unlock(pass)
		if err != nil {
			t.Fatal(err)
		}
		k.Lock()
		raw := s.owner.pass
		if len(raw) == 0 {
			t.Fatal("missing cache")
		}
		clear()
		if _, ok := s.OwnerRemaining(); ok || !bytes.Equal(raw, make([]byte, len(raw))) || s.owner.pass != nil {
			t.Fatal("unlock state survived lock or expiry")
		}
	}
}
