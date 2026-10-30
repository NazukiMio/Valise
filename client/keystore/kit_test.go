package keystore

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKitRoundTrip(t *testing.T) {
	k, data := sealed(t)
	dir := filepath.Join(t.TempDir(), "kit")
	if err := ExportKit(dir, data, k.Recipient()); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{KeystoreFile, RecipientFile, FingerprintFile, RecoveryDocFile} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatal(err)
		}
	}
	doc, _ := os.ReadFile(filepath.Join(dir, RecoveryDocFile))
	if !strings.Contains(string(doc), k.Fingerprint()) || !strings.Contains(string(doc), "tools/standalone-restore/") {
		t.Fatal("recovery doc incomplete")
	}
	kit, err := ImportKit(dir, pass)
	if err != nil {
		t.Fatal(err)
	}
	if kit.Fingerprint != k.Fingerprint() || kit.Key.Recipient() != k.Recipient() {
		t.Fatal("kit mismatch")
	}
	if err := ExportKit(dir, data, k.Recipient()); !errors.Is(err, ErrKitExists) {
		t.Fatalf("overwrite: %v", err)
	}
}

func TestKitFingerprintMismatch(t *testing.T) {
	k, data := sealed(t)
	dir := filepath.Join(t.TempDir(), "kit")
	if err := ExportKit(dir, data, k.Recipient()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, FingerprintFile), []byte("0000000000000000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportKit(dir, pass); !errors.Is(err, ErrKitFingerprint) {
		t.Fatalf("err = %v", err)
	}
}

func TestKitIdentityMismatch(t *testing.T) {
	k, data := sealed(t)
	other, _ := Generate()
	dir := filepath.Join(t.TempDir(), "kit")
	// recipient.txt and FINGERPRINT agree with each other but not with the
	// identity inside keystore.age.
	if err := ExportKit(dir, data, other.Recipient()); err != nil {
		t.Fatal(err)
	}
	_ = k
	if _, err := ImportKit(dir, pass); !errors.Is(err, ErrKitIdentityMatch) {
		t.Fatalf("err = %v", err)
	}
}
