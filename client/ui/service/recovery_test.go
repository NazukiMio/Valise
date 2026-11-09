package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	"filippo.io/age/agessh"

	core "valise/core/recovery"
)

// Changing the passphrase keeps the key; the kit export is remembered.
func TestChangePassphraseAndKeyBackup(t *testing.T) {
	s := newService(t, "ssh")
	if _, err := s.KeyInit(pass); err != nil {
		t.Fatal(err)
	}
	if s.KeyBackupDecided() {
		t.Fatal("decided before anything")
	}
	if _, err := s.ExportKit(filepath.Join(t.TempDir(), "kit")); err != nil {
		t.Fatal(err)
	}
	if b := s.KeyBackupState(); b.ExportedAt == nil || !s.KeyBackupDecided() {
		t.Fatalf("%+v", b)
	}
	before, _ := s.KeyShow()
	wantCode(t, s.ChangePassphrase("wrong passphrase!!", "another long passphrase"), "passphrase_wrong")
	if err := s.ChangePassphrase(pass, "another long passphrase"); err != nil {
		t.Fatal(err)
	}
	k, err := s.Unlock("another long passphrase")
	if err != nil || k.Recipient() != before.Recipient {
		t.Fatalf("%v", err)
	}
	k.Lock()
	if _, err := s.Unlock(pass); err == nil {
		t.Fatal("old passphrase still opens")
	}
}

// The backup lock end to end over fake-ssh: the lock is sealed to the
// server's host key; a request placed with the user pass is answered as
// the sudo command would, and the code and a new passphrase open the key.
func TestFindPassphraseAgain(t *testing.T) {
	s, _, home := addVPS(t, "")
	ctx := context.Background()
	if _, err := s.KeyInit(pass); err != nil {
		t.Fatal(err)
	}
	// A host key whose private half the test holds.
	kd := t.TempDir()
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", filepath.Join(kd, "hk")).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	priv, _ := os.ReadFile(filepath.Join(kd, "hk"))
	pub, _ := os.ReadFile(filepath.Join(kd, "hk.pub"))
	f := strings.Fields(string(pub))
	field := knownHostsField("203.0.113.9", 2222)
	if err := s.writeKnownHosts(field, []string{field + " " + f[0] + " " + f[1]}); err != nil {
		t.Fatal(err)
	}
	locks, err := s.EnableRecovery(pass)
	if err != nil || len(locks) != 1 || locks[0].State != LockDone {
		t.Fatalf("%+v %v", locks, err)
	}
	if !s.RecoveryEnabled() {
		t.Fatal("not enabled")
	}
	before, _ := s.KeyShow()

	c, err := s.CheckPorter(ctx, "vps")
	if err != nil || !c.Match || c.Expected == "" {
		t.Fatalf("%+v %v", c, err)
	}
	run, err := s.StartRecovery(ctx, "vps")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(run.Command, "sha256sum -c --quiet") || !strings.Contains(run.Command, "recovery-code --for ") || !strings.Contains(run.Command, c.Expected) {
		t.Fatalf("command %s", run.Command)
	}
	wantCode(t, s.VerifyRecovery(ctx, run, "12345678"), core.WaitingCode)

	// What `sudo valise-porter recovery-code` does on the server.
	dir := filepath.Join(home, core.Dir)
	b, _ := os.ReadFile(filepath.Join(dir, run.ID+".request"))
	var req struct {
		Lock      []byte `json:"lock"`
		Recipient string `json:"recipient"`
	}
	if err := jsonUnmarshal(b, &req); err != nil {
		t.Fatal(err)
	}
	id, _ := agessh.ParseIdentity(priv)
	dec, err := age.Decrypt(bytes.NewReader(req.Lock), id)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := io.ReadAll(dec)
	to, _ := age.ParseX25519Recipient(req.Recipient)
	ct, _ := sealTo(to, string(r))
	os.WriteFile(filepath.Join(dir, run.ID+".result"), ct, 0o600)
	code := core.Code(run.ID, ct)

	wantCode(t, s.VerifyRecovery(ctx, run, "00000000"), "recovery_code_wrong")
	if err := s.VerifyRecovery(ctx, run, code[:4]+" "+code[4:]); err != nil {
		t.Fatal(err)
	}
	done, err := s.FinishRecovery(ctx, run, "a brand new passphrase")
	if err != nil || len(done.Locks) != 1 || done.Locks[0].State != LockDone {
		t.Fatalf("%+v %v", done, err)
	}
	k, err := s.Unlock("a brand new passphrase")
	if err != nil || k.Recipient() != before.Recipient {
		t.Fatalf("key changed: %v", err)
	}
	k.Lock()
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Fatalf("request left on the server: %v", left)
	}

	// Three wrong codes end a request.
	run, _ = s.StartRecovery(ctx, "vps")
	os.WriteFile(filepath.Join(dir, run.ID+".result"), []byte("x"), 0o600)
	s.VerifyRecovery(ctx, run, "1")
	s.VerifyRecovery(ctx, run, "2")
	wantCode(t, s.VerifyRecovery(ctx, run, "3"), "recovery_void")

	// A changed host key shows; turning the lock off deletes its files.
	other := field + " ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl"
	s.writeKnownHosts(field, []string{other})
	if l := s.ServerLocks(); l[0].State != LockChanged {
		t.Fatalf("%+v", l)
	}
	if err := s.DisableRecovery(); err != nil || s.RecoveryEnabled() {
		t.Fatal("still on")
	}
}

// Forgetting the admin passphrase: every admin pass is rebuilt with a new
// one; the old line goes from authorized_keys.
func TestRebuildAdminPasses(t *testing.T) {
	s, _, home := addVPS(t, "the admin passphrase")
	oldPub, _ := os.ReadFile(s.keyPath("vps", adminKeyName) + ".pub")
	var got []AdminRebuild
	if err := s.RebuildAdminPasses(context.Background(), "a new admin passphrase", func(r AdminRebuild) { got = append(got, r) }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].Done || s.AdminRebuildPending("vps") || !s.HasAdminKey("vps") {
		t.Fatalf("%+v", got)
	}
	newPub, _ := os.ReadFile(s.keyPath("vps", adminKeyName) + ".pub")
	ak, _ := os.ReadFile(filepath.Join(home, ".ssh", "authorized_keys"))
	if strings.Contains(string(ak), strings.Fields(string(oldPub))[1]) || !strings.Contains(string(ak), strings.Fields(string(newPub))[1]) {
		t.Fatalf("authorized_keys:\n%s", ak)
	}
}

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }
