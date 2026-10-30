// Package minisign verifies minisign signatures. Only the file formats are
// parsed here; signatures are checked with crypto/ed25519 and the prehash
// (algorithm "ED") uses golang.org/x/crypto/blake2b.
package minisign

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"strings"

	"golang.org/x/crypto/blake2b"
)

const (
	untrustedPrefix = "untrusted comment: "
	trustedPrefix   = "trusted comment: "
)

// Signature algorithms: legacy pure Ed25519 and BLAKE2b-512 prehashed.
var (
	algEd = [2]byte{'E', 'd'}
	algED = [2]byte{'E', 'D'}
)

var (
	ErrFormat    = errors.New("minisign: malformed key or signature")
	ErrKeyID     = errors.New("minisign: signature was made with a different key")
	ErrInvalid   = errors.New("minisign: signature verification failed")
	ErrTrusted   = errors.New("minisign: trusted comment signature verification failed")
	ErrAlgorithm = errors.New("minisign: unsupported signature algorithm")
)

// PublicKey is a minisign public key.
type PublicKey struct {
	KeyID [8]byte
	Key   ed25519.PublicKey
}

// ParsePublicKey accepts the base64 key line, or a .pub file whose first
// line is an untrusted comment.
func ParsePublicKey(s string) (PublicKey, error) {
	var pk PublicKey
	lines := strings.Split(strings.TrimSpace(s), "\n")
	line := strings.TrimSpace(lines[len(lines)-1])
	if len(lines) > 2 || (len(lines) == 2 && !strings.HasPrefix(lines[0], untrustedPrefix)) {
		return pk, ErrFormat
	}
	b, err := base64.StdEncoding.DecodeString(line)
	if err != nil || len(b) != 2+8+ed25519.PublicKeySize || [2]byte(b[:2]) != algEd {
		return pk, ErrFormat
	}
	copy(pk.KeyID[:], b[2:10])
	pk.Key = ed25519.PublicKey(bytes.Clone(b[10:]))
	return pk, nil
}

// Signature is a parsed .minisig file.
type Signature struct {
	Algorithm      [2]byte
	KeyID          [8]byte
	Sig            [ed25519.SignatureSize]byte
	TrustedComment string
	GlobalSig      [ed25519.SignatureSize]byte
}

// ParseSignature parses the four-line .minisig format.
func ParseSignature(data []byte) (Signature, error) {
	var s Signature
	lines := strings.Split(strings.TrimRight(string(data), "\r\n"), "\n")
	if len(lines) != 4 || !strings.HasPrefix(lines[0], untrustedPrefix) || !strings.HasPrefix(lines[2], trustedPrefix) {
		return s, ErrFormat
	}
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lines[1]))
	if err != nil || len(b) != 2+8+ed25519.SignatureSize {
		return s, ErrFormat
	}
	copy(s.Algorithm[:], b[:2])
	copy(s.KeyID[:], b[2:10])
	copy(s.Sig[:], b[10:])
	s.TrustedComment = strings.TrimSuffix(strings.TrimPrefix(lines[2], trustedPrefix), "\r")
	g, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lines[3]))
	if err != nil || len(g) != ed25519.SignatureSize {
		return s, ErrFormat
	}
	copy(s.GlobalSig[:], g)
	return s, nil
}

// Verify checks message against a .minisig file, including the signature
// over the trusted comment. It returns the trusted comment.
func Verify(pk PublicKey, message, sigFile []byte) (string, error) {
	s, err := ParseSignature(sigFile)
	if err != nil {
		return "", err
	}
	if s.KeyID != pk.KeyID {
		return "", ErrKeyID
	}
	var signed []byte
	switch s.Algorithm {
	case algED:
		h := blake2b.Sum512(message)
		signed = h[:]
	case algEd:
		signed = message
	default:
		return "", ErrAlgorithm
	}
	if !ed25519.Verify(pk.Key, signed, s.Sig[:]) {
		return "", ErrInvalid
	}
	global := append(bytes.Clone(s.Sig[:]), s.TrustedComment...)
	if !ed25519.Verify(pk.Key, global, s.GlobalSig[:]) {
		return "", ErrTrusted
	}
	return s.TrustedComment, nil
}
