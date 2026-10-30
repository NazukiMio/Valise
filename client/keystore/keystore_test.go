package keystore

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"filippo.io/age"
)

const pass = "correct horse battery"

func sealed(t *testing.T) (*UnlockedKey, []byte) {
	t.Helper()
	k, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	data, err := Seal(k, pass)
	if err != nil {
		t.Fatal(err)
	}
	return k, data
}

func TestRoundTrip(t *testing.T) {
	k, data := sealed(t)
	if !bytes.HasPrefix(data, []byte("age-encryption.org/v1\n-> scrypt ")) {
		t.Fatalf("not an age scrypt file: %q", data[:40])
	}
	u, err := Unlock(data, pass)
	if err != nil {
		t.Fatal(err)
	}
	if u.Recipient() != k.Recipient() || u.Fingerprint() != k.Fingerprint() || len(u.Fingerprint()) != 16 {
		t.Fatalf("recipient mismatch")
	}
	// The unlocked identity decrypts what the recipient encrypted.
	id, err := u.Identity()
	if err != nil {
		t.Fatal(err)
	}
	rcpt, _ := age.ParseX25519Recipient(k.Recipient())
	var ct bytes.Buffer
	w, _ := age.Encrypt(&ct, rcpt)
	w.Write([]byte("soul"))
	w.Close()
	r, err := age.Decrypt(&ct, id)
	if err != nil {
		t.Fatal(err)
	}
	var pt bytes.Buffer
	pt.ReadFrom(r)
	if pt.String() != "soul" {
		t.Fatal(pt.String())
	}
}

func TestWrongPassphrase(t *testing.T) {
	_, data := sealed(t)
	if _, err := Unlock(data, "wrong passphrase!"); !errors.Is(err, ErrWrongPassphrase) {
		t.Fatalf("err = %v", err)
	}
}

func TestPassphraseLength(t *testing.T) {
	k, _ := Generate()
	if _, err := Seal(k, "elevenchars"); !errors.Is(err, ErrPassphraseTooShort) {
		t.Fatalf("11 chars: %v", err)
	}
	// Counted in Unicode characters, not bytes: 12 CJK characters pass.
	if err := CheckPassphrase("一二三四五六七八九十甲乙"); err != nil {
		t.Fatal(err)
	}
	if err := CheckPassphrase("一二三四五六七八九十甲"); err == nil {
		t.Fatal("11 CJK characters accepted")
	}
}

func TestTamperDetected(t *testing.T) {
	_, data := sealed(t)
	// Flip a byte in the payload (end of file) and in the header MAC line.
	payload := bytes.Clone(data)
	payload[len(payload)-5] ^= 0x01
	if _, err := Unlock(payload, pass); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("payload tamper: %v", err)
	}
	header := bytes.Clone(data)
	i := bytes.Index(header, []byte("\n--- ")) + 6
	header[i] ^= 0x01
	if _, err := Unlock(header, pass); err == nil {
		t.Fatal("header tamper not detected")
	}
	if _, err := Unlock(data[:len(data)-10], pass); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("truncation: %v", err)
	}
}

func TestLock(t *testing.T) {
	k, _ := Generate()
	secret := k.secret
	k.Lock()
	if !k.Locked() || bytes.ContainsFunc(secret, func(r rune) bool { return r != 0 }) {
		t.Fatal("secret not zeroed")
	}
	if _, err := k.Identity(); !errors.Is(err, ErrLocked) {
		t.Fatal(err)
	}
	if _, err := Seal(k, pass); !errors.Is(err, ErrLocked) {
		t.Fatal(err)
	}
}

func TestImport(t *testing.T) {
	id, _ := age.GenerateX25519Identity()
	file := "# created: x\n# public key: " + id.Recipient().String() + "\n" + id.String() + "\n"
	for _, in := range []string{id.String(), file} {
		k, err := Import([]byte(in))
		if err != nil || k.Recipient() != id.Recipient().String() {
			t.Fatalf("%q: %v", in, err)
		}
	}
	other, _ := age.GenerateX25519Identity()
	for _, bad := range []string{"", "# only comments\n", "not a key", id.String() + "\n" + other.String()} {
		if _, err := Import([]byte(bad)); !errors.Is(err, ErrNotIdentity) {
			t.Fatalf("%q: %v", bad, err)
		}
	}
}

// keystore.age must open with the standard age CLI. age reads passphrases
// only from a terminal, so the test drives it through script(1).
func TestStandardAgeCLIDecrypts(t *testing.T) {
	agePath, err := exec.LookPath("age")
	if err != nil {
		t.Skip("age CLI not installed")
	}
	if _, err := exec.LookPath("script"); err != nil {
		t.Skip("script(1) not available to provide a terminal")
	}
	k, data := sealed(t)
	dir := t.TempDir()
	ks, out := filepath.Join(dir, KeystoreFile), filepath.Join(dir, "identity.txt")
	if err := SaveFile(ks, data); err != nil {
		t.Fatal(err)
	}
	ageCmd := agePath + " -d -o " + out + " " + ks
	var cmd *exec.Cmd
	if runtime.GOOS == "linux" {
		cmd = exec.Command("script", "-qec", ageCmd, "/dev/null")
	} else {
		cmd = exec.Command("script", "-q", "/dev/null", agePath, "-d", "-o", out, ks)
	}
	cmd.Stdin = strings.NewReader(pass + "\n")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("age -d: %v\n%s", err, b)
	}
	plain, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Import(plain)
	if err != nil || got.Recipient() != k.Recipient() {
		t.Fatalf("age CLI output is not the identity: %v", err)
	}
	if !strings.Contains(string(plain), "# public key: "+k.Recipient()) {
		t.Fatalf("missing public key comment")
	}
}

func TestSaveFileMode(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", KeystoreFile)
	if err := SaveFile(p, []byte("x")); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(p)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v %v", st.Mode(), err)
	}
}
