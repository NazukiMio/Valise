package seal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"valise/core/valise"
)

func TestVaultLockStructuredJSONAndBusyMessage(t *testing.T) {
	p := Paths{Home: t.TempDir()}
	unlock, err := p.LockFor("terminal-action")
	if err != nil {
		t.Fatalf("LockFor: %v", err)
	}
	defer unlock()

	lockPath := filepath.Join(p.State(), "vault.lock")
	raw, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read vault.lock: %v", err)
	}
	var info lockInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		t.Fatalf("expected JSON in vault.lock, got %q: %v", string(raw), err)
	}
	if info.Holder != "terminal-action" || info.PID != os.Getpid() {
		t.Fatalf("unexpected lock info: %+v", info)
	}
	parsedTime, err := time.Parse(time.RFC3339, info.StartedAt)
	if err != nil {
		t.Fatalf("invalid started_at %q: %v", info.StartedAt, err)
	}

	_, err = p.LockFor("seal")
	ue, ok := valise.As(err)
	if !ok || ue.Code != "vault_busy" {
		t.Fatalf("expected vault_busy, got %v", err)
	}
	wantPID := "PID " + strconv.Itoa(os.Getpid())
	wantTime := "始于 " + parsedTime.Local().Format("15:04")
	for _, sub := range []string{"终端管理命令", wantPID, wantTime} {
		if !strings.Contains(ue.Reason, sub) {
			t.Fatalf("busy reason %q missing %q", ue.Reason, sub)
		}
	}
	if strings.Contains(ue.Reason, "封存或同步") {
		t.Fatalf("busy reason should not blame seal/sync generically: %q", ue.Reason)
	}
	if !strings.Contains(ue.Suggestion, "检查并结束该进程") {
		t.Fatalf("unexpected suggestion: %q", ue.Suggestion)
	}
}

func TestVaultLockActionPrefixAndLegacyPlainText(t *testing.T) {
	// ActionLock prefix.
	p := Paths{Home: t.TempDir()}
	f, unlock, err := p.ActionLock("op_12345")
	if err != nil {
		t.Fatalf("ActionLock: %v", err)
	}
	_ = f
	_, err = p.Lock()
	ue, ok := valise.As(err)
	if !ok || ue.Code != "vault_busy" || !strings.Contains(ue.Reason, "管理动作 op_12345") {
		t.Fatalf("expected action prefix in busy reason, got %v", err)
	}
	unlock()

	// Legacy plain-text lock file without JSON fields.
	p2 := Paths{Home: t.TempDir()}
	if err := os.MkdirAll(p2.State(), 0o700); err != nil {
		t.Fatal(err)
	}
	lf, err := os.OpenFile(filepath.Join(p2.State(), "vault.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer lf.Close()
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	if _, err := lf.WriteAt([]byte("native"), 0); err != nil {
		t.Fatal(err)
	}
	_, err = p2.Lock()
	ue, ok = valise.As(err)
	if !ok || ue.Code != "vault_busy" {
		t.Fatalf("expected vault_busy, got %v", err)
	}
	if ue.Reason != "服务器正被「命令行与向导」占用" {
		t.Fatalf("unexpected legacy busy reason: %q", ue.Reason)
	}
}

func TestVaultUnlockPreservesChildPIDMetadata(t *testing.T) {
	p := Paths{Home: t.TempDir()}
	unlock, err := p.LockFor("terminal-action")
	if err != nil {
		t.Fatalf("LockFor: %v", err)
	}
	lockPath := filepath.Join(p.State(), "vault.lock")

	// Simulate an inherited child updating vault.lock with its own PID.
	childPayload, _ := json.Marshal(lockInfo{
		Holder:    "action:op_child",
		PID:       os.Getpid() + 99999,
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	})
	if err := os.WriteFile(lockPath, childPayload, 0o600); err != nil {
		t.Fatal(err)
	}

	unlock()

	got, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != string(childPayload) {
		t.Fatalf("unlock truncated another PID's lock metadata: got %q, want %q", string(got), string(childPayload))
	}

	// Now acquire normally and unlock: own PID should be truncated.
	u2, err := p.LockFor("seal")
	if err != nil {
		t.Fatal(err)
	}
	u2()
	got2, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(got2) != 0 {
		t.Fatalf("expected unlock to truncate own PID metadata, got %q", string(got2))
	}
}
