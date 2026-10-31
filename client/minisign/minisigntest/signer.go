// Package minisigntest produces minisign keys and signatures for tests in
// other packages. Valise itself only verifies; signing exists for tests and
// is done with the minisign tool at release time.
package minisigntest

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"

	"golang.org/x/crypto/blake2b"
)

// Signer signs with a fresh key.
type Signer struct {
	priv  ed25519.PrivateKey
	keyID [8]byte
	// PublicKey is the .pub file content.
	PublicKey string
}

// New generates a key.
func New() *Signer {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	s := &Signer{priv: priv}
	rand.Read(s.keyID[:])
	b := append([]byte("Ed"), s.keyID[:]...)
	s.PublicKey = "untrusted comment: minisign public key\n" + base64.StdEncoding.EncodeToString(append(b, pub...)) + "\n"
	return s
}

// Sign returns a prehashed (ED) .minisig file for msg.
func (s *Signer) Sign(msg []byte, trusted string) []byte {
	h := blake2b.Sum512(msg)
	sig := ed25519.Sign(s.priv, h[:])
	line := append(append([]byte("ED"), s.keyID[:]...), sig...)
	global := ed25519.Sign(s.priv, append(sig, trusted...))
	return []byte("untrusted comment: signature\n" + base64.StdEncoding.EncodeToString(line) + "\n" +
		"trusted comment: " + trusted + "\n" + base64.StdEncoding.EncodeToString(global) + "\n")
}
