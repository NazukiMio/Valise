package forced

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// One-time grants (docs/ARCHITECTURE.md 2.2): an owner session lets the
// daily key run one subcommand that is not RPC (stage-write, login-run)
// for one operation, once, within grantTTL.

const grantTTL = 5 * time.Minute

// GrantSubcommands are the subcommands the daily key runs with a grant;
// the value is the index of the argument the grant is bound to.
var GrantSubcommands = map[string]int{"stage-write": 2, "login-run": 1, "tool-put": 1}

// ErrGrant is returned for a missing, used, expired or mismatched grant.
var ErrGrant = errors.New("这一步的许可无效或已过期；请在 Valise 里重新开始这一步")

type grant struct {
	Command string    `json:"command"`
	Arg     string    `json:"arg"`
	Expires time.Time `json:"expires"`
}

func grantDir(home string) string { return filepath.Join(home, ".local", "state", "valise", "grants") }

// Mint records a grant for command bound to arg and returns its token.
func Mint(home, command, arg string) (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)
	if err := os.MkdirAll(grantDir(home), 0o700); err != nil {
		return "", err
	}
	data, _ := json.Marshal(grant{command, arg, time.Now().Add(grantTTL)})
	return token, os.WriteFile(filepath.Join(grantDir(home), token), data, 0o600)
}

// Consume checks and spends the grant for command bound to arg.
func Consume(home, token, command, arg string) error {
	if _, err := hex.DecodeString(token); err != nil || len(token) != 48 {
		return ErrGrant
	}
	path := filepath.Join(grantDir(home), token)
	data, err := os.ReadFile(path)
	if err != nil {
		return ErrGrant
	}
	if err := os.Remove(path); err != nil {
		return ErrGrant
	}
	var g grant
	if json.Unmarshal(data, &g) != nil || g.Command != command || g.Arg != arg || time.Now().After(g.Expires) {
		return ErrGrant
	}
	return nil
}

// TakeGrant splits a daily command line's trailing "--grant <token>" from
// a grant subcommand's arguments.
func TakeGrant(words []string) (args []string, token string, ok bool) {
	n := len(words)
	if n < 3 || words[n-2] != "--grant" {
		return words, "", false
	}
	return words[:n-2], words[n-1], true
}
