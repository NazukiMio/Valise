package minisign

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/blake2b"
)

// testSigner produces minisign files; signing lives only in tests.
type testSigner struct {
	priv  ed25519.PrivateKey
	keyID [8]byte
}

func newSigner(t *testing.T) (*testSigner, string) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s := &testSigner{priv: priv}
	rand.Read(s.keyID[:])
	b := append([]byte("Ed"), s.keyID[:]...)
	return s, "untrusted comment: minisign public key\n" + base64.StdEncoding.EncodeToString(append(b, pub...)) + "\n"
}

func (s *testSigner) sign(msg []byte, prehash bool, trusted string) []byte {
	alg, signed := "Ed", msg
	if prehash {
		h := blake2b.Sum512(msg)
		alg, signed = "ED", h[:]
	}
	sig := ed25519.Sign(s.priv, signed)
	line := append(append([]byte(alg), s.keyID[:]...), sig...)
	global := ed25519.Sign(s.priv, append(sig, trusted...))
	return []byte("untrusted comment: signature\n" + base64.StdEncoding.EncodeToString(line) + "\n" +
		"trusted comment: " + trusted + "\n" + base64.StdEncoding.EncodeToString(global) + "\n")
}

func TestVerify(t *testing.T) {
	s, pubFile := newSigner(t)
	pk, err := ParsePublicKey(pubFile)
	if err != nil {
		t.Fatal(err)
	}
	msg := []byte("abc  valise-porter_linux_arm64\n")
	for _, prehash := range []bool{true, false} {
		sig := s.sign(msg, prehash, "timestamp:1 file:SHA256SUMS")
		tc, err := Verify(pk, msg, sig)
		if err != nil || tc != "timestamp:1 file:SHA256SUMS" {
			t.Fatalf("prehash=%v: %v", prehash, err)
		}
		if _, err := Verify(pk, append(msg, 'x'), sig); !errors.Is(err, ErrInvalid) {
			t.Fatalf("modified message: %v", err)
		}
		forged := strings.Replace(string(sig), "file:SHA256SUMS", "file:other", 1)
		if _, err := Verify(pk, msg, []byte(forged)); !errors.Is(err, ErrTrusted) {
			t.Fatalf("forged trusted comment: %v", err)
		}
	}
	// The bare key line is accepted too.
	if _, err := ParsePublicKey(strings.Split(pubFile, "\n")[1]); err != nil {
		t.Fatal(err)
	}
}

func TestWrongKey(t *testing.T) {
	s, _ := newSigner(t)
	_, otherPub := newSigner(t)
	pk, _ := ParsePublicKey(otherPub)
	if _, err := Verify(pk, []byte("m"), s.sign([]byte("m"), true, "t")); !errors.Is(err, ErrKeyID) {
		t.Fatalf("err = %v", err)
	}
}

func TestMalformed(t *testing.T) {
	for _, bad := range []string{"", "garbage", "untrusted comment: x\nAAAA\n"} {
		if _, err := ParsePublicKey(bad); !errors.Is(err, ErrFormat) {
			t.Errorf("key %q: %v", bad, err)
		}
		if _, err := ParseSignature([]byte(bad)); !errors.Is(err, ErrFormat) {
			t.Errorf("sig %q: %v", bad, err)
		}
	}
}

// testdata was produced by the minisign 0.11 CLI with a throwaway key:
// `minisign -S` (prehashed, the default) and `minisign -S -l` (legacy).
func TestRealMinisignVectors(t *testing.T) {
	read := func(name string) []byte {
		b, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	pk, err := ParsePublicKey(string(read("test.pub")))
	if err != nil {
		t.Fatal(err)
	}
	for name, comment := range map[string]string{"SHA256SUMS": "valise test vector", "legacy.txt": "legacy"} {
		tc, err := Verify(pk, read(name), read(name+".minisig"))
		if err != nil || tc != comment {
			t.Fatalf("%s: %q %v", name, tc, err)
		}
	}
	if _, err := Verify(pk, read("legacy.txt"), read("SHA256SUMS.minisig")); err != nil {
		t.Fatal("identical content must verify with either signature:", err)
	}
	tampered := append(read("SHA256SUMS"), '\n')
	if _, err := Verify(pk, tampered, read("SHA256SUMS.minisig")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("tampered: %v", err)
	}
}
