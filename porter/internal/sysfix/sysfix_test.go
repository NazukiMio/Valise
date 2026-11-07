package sysfix

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"valise/core/install"
	porterenroll "valise/porter/internal/enroll"
	"valise/porter/internal/seal"
)

func fakeEnv(t *testing.T, tools map[string]string) (*porterenroll.Env, string) {
	t.Helper()
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	os.MkdirAll(bin, 0o755)
	for name, body := range tools {
		os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body), 0o755)
	}
	return &porterenroll.Env{Home: home, Vars: []string{"PATH=" + bin, "USER=alice", "HOME=" + home}}, home
}

func TestRead(t *testing.T) {
	old := LingerDir
	t.Cleanup(func() { LingerDir = old })
	LingerDir = filepath.Join(t.TempDir(), "absent")
	e, home := fakeEnv(t, map[string]string{
		"loginctl":    "echo no\n",
		"timedatectl": "printf 'CanNTP=yes\\nNTP=no\\nNTPSynchronized=no\\n'\n",
		"systemctl":   "echo loaded\n",
	})
	st := Read(context.Background(), e, home)
	if st.User != "alice" || st.Linger != No || st.CanNTP != Yes || st.NTP != No || st.NTPSynchronized != No || st.Timesyncd != "loaded" || st.Now.IsZero() {
		t.Fatalf("%+v", st)
	}
	// The linger directory is authoritative when present.
	LingerDir = t.TempDir()
	os.WriteFile(filepath.Join(LingerDir, "alice"), nil, 0o644)
	if st := Read(context.Background(), e, home); st.Linger != Yes {
		t.Fatalf("%+v", st)
	}
	// Failed reads are unknown, never "no".
	e, home = fakeEnv(t, map[string]string{"timedatectl": "exit 1\n"})
	LingerDir = filepath.Join(t.TempDir(), "absent")
	st = Read(context.Background(), e, home)
	if st.Linger != Unknown || st.NTP != Unknown || st.NTPSynchronized != Unknown || st.Timesyncd != Unknown {
		t.Fatalf("%+v", st)
	}
}

func TestValidUser(t *testing.T) {
	for u, ok := range map[string]bool{"alice": true, "nazukimio": true, "a-b.c_1": true, "x$": true,
		"": false, "-a": false, "a b": false, "a;rm": false, "Root": false, "1a": false} {
		if ValidUser(u) != ok {
			t.Errorf("%q", u)
		}
	}
}

func TestAudit(t *testing.T) {
	home := t.TempDir()
	if err := Audit(home, AuditParams{Fix: "linger", Mode: "direct", Instance: "demo", Result: "resolved",
		Steps: []install.StepAudit{{Kind: "system", Command: "loginctl enable-linger alice", Exit: 0}}}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(seal.Paths{Home: home}.Audit())
	if err != nil || !strings.Contains(string(b), `"op":"system_fix"`) || !strings.Contains(string(b), "loginctl enable-linger alice exit 0") {
		t.Fatalf("%s %v", b, err)
	}
	for _, p := range []AuditParams{
		{Fix: "rm", Mode: "direct", Result: "resolved"},
		{Fix: "clock", Mode: "root", Result: "resolved"},
		{Fix: "clock", Mode: "advanced", Result: "ok"},
		{Fix: "clock", Mode: "advanced", Result: "failed", Steps: []install.StepAudit{{Command: "a\nb"}}},
	} {
		if Audit(home, p) == nil {
			t.Errorf("accepted %+v", p)
		}
	}
}
