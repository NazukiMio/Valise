// Package recovery is the shared part of finding the Valise passphrase
// again with the backup lock (docs/ARCHITECTURE.md 3.2): the porter
// methods that place and fetch a request in the user's own staging
// folder, and the verification code both sides compute.
package recovery

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"regexp"
)

// Porter methods (daily tier: they only touch the user's own folder).
const (
	MethodRequest = "recovery_request"
	MethodResult  = "recovery_result"
	MethodClear   = "recovery_clear"
)

// Dir is the staging folder under the user's home.
const Dir = ".local/state/valise/recovery"

// MaxTries is how many wrong codes end a request (2026-11-09 user decision).
const MaxTries = 3

var idRe = regexp.MustCompile(`^[0-9a-f]{32}$`)

// ValidID reports whether id is a request id: 32 lowercase hex digits.
func ValidID(id string) bool { return idRe.MatchString(id) }

// RequestParams places a request: the server's copy of the backup lock
// (R sealed to the host key) and the one-time recipient E to answer to.
type RequestParams struct {
	ID        string `json:"id"`
	Lock      []byte `json:"lock"`
	Recipient string `json:"recipient"`
}

// RequestResult says what the sudo command needs: the account to answer
// for and the deployed porter's path.
type RequestResult struct {
	User   string `json:"user"`
	Porter string `json:"porter"`
}

// IDParams names a request.
type IDParams struct {
	ID string `json:"id"`
}

// ResultResult is R sealed to E, written by `recovery-code`.
type ResultResult struct {
	Ciphertext []byte `json:"ciphertext"`
}

// WaitingCode is the error code while the sudo command has not run yet.
const WaitingCode = "recovery_waiting"

// Code is the 8-digit verification code for a request's answer, shown by
// the sudo command and typed back into Valise.
func Code(id string, ciphertext []byte) string {
	h := sha256.New()
	h.Write([]byte("valise-recovery-code/1\x00" + id + "\x00"))
	h.Write(ciphertext)
	n := binary.BigEndian.Uint64(h.Sum(nil)[:8]) % 100000000
	return fmt.Sprintf("%08d", n)
}
