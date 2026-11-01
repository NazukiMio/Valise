package service

import (
	"context"
	"os"
	"os/exec"
	"testing"

	"valise/client/config"
	"valise/core/valise"
)

// An admin key that has left the agent (the window expired) is asked for
// again before the next admin step (docs/INSTALL.md 4.1).
func TestAdminWindowExpiredAsksAgain(t *testing.T) {
	if _, err := exec.LookPath("ssh-agent"); err != nil {
		t.Skip("no ssh-agent")
	}
	s := &Service{Dirs: config.Dirs{Data: t.TempDir(), Cache: t.TempDir()}}
	defer s.Close()
	k := s.keyPath("h", adminKeyName)
	os.MkdirAll(config.KeyDir(s.Dirs.Data, "h"), 0o700)
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "correct-horse-1", "-f", k).CombinedOutput(); err != nil {
		t.Fatal(string(out))
	}
	ctx := context.Background()
	if err := s.loadAdmin(ctx, "h", "correct-horse-1"); err != nil {
		t.Fatal(err)
	}
	asked := 0
	s.AdminAuth.Passphrase = func(string) (string, error) { asked++; return "wrong-pass-123", nil }
	if _, err := s.unlockAdmin(ctx, "h"); err != nil || asked != 0 {
		t.Fatalf("loaded: %v asked=%d", err, asked)
	}
	sock, _ := s.agentSock(ctx)
	if out, err := exec.Command("env", "SSH_AUTH_SOCK="+sock, "ssh-add", "-D").CombinedOutput(); err != nil {
		t.Fatal(string(out))
	}
	_, err := s.unlockAdmin(ctx, "h")
	if ue, ok := valise.As(err); !ok || ue.Code != "admin_passphrase_wrong" || asked != 1 {
		t.Fatalf("after expiry: %v asked=%d", err, asked)
	}
}
