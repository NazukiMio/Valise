// Package keystore manages the user's age X25519 identity. The identity is
// stored as keystore.age, a standard age passphrase-encrypted (scrypt) file
// whose plaintext is a regular age identity file, so `age -d keystore.age`
// recovers it without Valise.
package keystore

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"filippo.io/age"
)

// MinPassphraseRunes is the minimum passphrase length in Unicode characters.
const MinPassphraseRunes = 12

// File names inside the client data directory and the recovery kit.
const (
	KeystoreFile  = "keystore.age"
	RecipientFile = "recipient.txt"
)

var (
	ErrPassphraseTooShort = fmt.Errorf("口令至少需要 %d 个字符", MinPassphraseRunes)
	ErrWrongPassphrase    = errors.New("口令错误，或 keystore.age 的头部已损坏")
	ErrCorrupt            = errors.New("keystore.age 已损坏或被篡改")
	ErrLocked             = errors.New("密钥已锁定")
	ErrNotIdentity        = errors.New("内容不是一个 age X25519 身份")
)

// UnlockedKey holds a decrypted identity. Lock zeroes the copy it owns on a
// best-effort basis: the Go runtime and age may keep other copies in memory,
// so a complete wipe cannot be guaranteed.
type UnlockedKey struct {
	secret    []byte // "AGE-SECRET-KEY-1…"
	recipient string
}

// Generate creates a new identity.
func Generate() (*UnlockedKey, error) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		return nil, err
	}
	return &UnlockedKey{secret: []byte(id.String()), recipient: id.Recipient().String()}, nil
}

// Import parses an existing identity: either a bare AGE-SECRET-KEY-1 line or
// an identity file with comments. Exactly one X25519 identity is accepted.
func Import(text []byte) (*UnlockedKey, error) {
	var found []byte
	sc := bufio.NewScanner(bytes.NewReader(text))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("%w：只能包含一个身份", ErrNotIdentity)
		}
		found = []byte(line)
	}
	if found == nil {
		return nil, ErrNotIdentity
	}
	id, err := age.ParseX25519Identity(string(found))
	if err != nil {
		return nil, ErrNotIdentity
	}
	return &UnlockedKey{secret: found, recipient: id.Recipient().String()}, nil
}

// Recipient returns the age1… public key.
func (k *UnlockedKey) Recipient() string { return k.recipient }

// Fingerprint returns the recipient fingerprint.
func (k *UnlockedKey) Fingerprint() string { return Fingerprint(k.recipient) }

// Identity returns the age identity for decryption.
func (k *UnlockedKey) Identity() (*age.X25519Identity, error) {
	if k.secret == nil {
		return nil, ErrLocked
	}
	return age.ParseX25519Identity(string(k.secret))
}

// Locked reports whether Lock was called.
func (k *UnlockedKey) Locked() bool { return k.secret == nil }

// Lock zeroes the secret this value holds and makes it unusable.
func (k *UnlockedKey) Lock() {
	for i := range k.secret {
		k.secret[i] = 0
	}
	k.secret = nil
}

// Fingerprint is the first 16 hex digits of SHA-256 over the recipient
// string (docs/ARCHITECTURE.md section 3).
func Fingerprint(recipient string) string {
	sum := sha256.Sum256([]byte(recipient))
	return hex.EncodeToString(sum[:])[:16]
}

// CheckPassphrase enforces the minimum length.
func CheckPassphrase(passphrase string) error {
	if utf8.RuneCountInString(passphrase) < MinPassphraseRunes {
		return ErrPassphraseTooShort
	}
	return nil
}

// Seal encrypts the identity with the passphrase using age's default scrypt
// work factor, which must not be lowered.
func Seal(k *UnlockedKey, passphrase string) ([]byte, error) {
	if err := CheckPassphrase(passphrase); err != nil {
		return nil, err
	}
	if k.Locked() {
		return nil, ErrLocked
	}
	r, err := age.NewScryptRecipient(passphrase)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, r)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(w, "# created: %s\n# public key: %s\n%s\n", time.Now().UTC().Format(time.RFC3339), k.recipient, k.secret)
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Unlock decrypts keystore.age contents.
func Unlock(data []byte, passphrase string) (*UnlockedKey, error) {
	id, err := age.NewScryptIdentity(passphrase)
	if err != nil {
		return nil, err
	}
	r, err := age.Decrypt(bytes.NewReader(data), id)
	if err != nil {
		var nm *age.NoIdentityMatchError
		if errors.As(err, &nm) {
			return nil, ErrWrongPassphrase
		}
		return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	plain, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	defer func() {
		for i := range plain {
			plain[i] = 0
		}
	}()
	k, err := Import(plain)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	return k, nil
}

// SaveFile writes data to path atomically with mode 0600.
func SaveFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	ok = true
	return nil
}
