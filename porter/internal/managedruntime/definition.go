// Package managedruntime provides the fixed user-service entry used by the
// parallel activation controller. It is not an RPC command runner. It never
// swaps data, accepts human confirmation, or declares gateway/dialogue success.
package managedruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"unicode"

	"valise/core/environment"
	"valise/core/identity"
	"valise/core/protection"
	"valise/porter/internal/isolated"
)

var ErrDefinition = errors.New("managed runtime definition is invalid or changed")
var ErrState = errors.New("managed runtime ownership or process state is unknown")
var ErrAction = errors.New("managed runtime service action did not complete")

// Definitions are compiled from a prepared workspace or reloaded by their
// recorded digest. The fields
// are private so a JSON request cannot provide unit contents or executable argv.
type Definition struct{ name, content, digest string }

func (d Definition) Name() string    { return d.name }
func (d Definition) Content() string { return d.content }
func (d Definition) Digest() string  { return d.digest }

func Compile(agent identity.AgentRef, w *isolated.Workspace, s isolated.Spec, data []isolated.DataBinding) (Definition, error) {
	if protection.ValidateAgent(agent) != nil {
		return Definition{}, ErrDefinition
	}
	args, err := isolated.ManagedArguments(w, s, data)
	if err != nil {
		return Definition{}, err
	}
	return compileDefinition(agent, args)
}

func compileDefinition(agent identity.AgentRef, args []string) (Definition, error) {
	if protection.ValidateAgent(agent) != nil {
		return Definition{}, ErrDefinition
	}
	argv := append([]string{"/usr/bin/bwrap"}, args...)
	var words []string
	for _, arg := range argv {
		quoted, err := systemdWord(arg)
		if err != nil {
			return Definition{}, err
		}
		words = append(words, quoted)
	}
	name := "valise-agent-" + environment.Key(agent) + ".service"
	text := "[Unit]\nDescription=Valise managed Agent\n\n[Service]\nType=exec\nExecStart=" + strings.Join(words, " ") + "\nWorkingDirectory=/\nEnvironment=HOME=/nonexistent PATH=/usr/bin:/bin LANG=C\nStandardInput=null\nStandardOutput=null\nStandardError=null\nNoNewPrivileges=yes\nLimitCORE=0\nKillMode=control-group\nTimeoutStartSec=30\nTimeoutStopSec=45\nRestart=on-failure\nRestartSec=5\n\n[Install]\nWantedBy=default.target\n"
	return Definition{name, text, digest(text)}, nil
}

func digest(text string) string { h := sha256.Sum256([]byte(text)); return hex.EncodeToString(h[:]) }

// ExecStart has its own expansion rules, distinct from shell and JSON. Quote
// every word, double systemd specifiers/variables, and reject control bytes.
func systemdWord(s string) (string, error) {
	if strings.IndexFunc(s, unicode.IsControl) >= 0 {
		return "", ErrDefinition
	}
	s = strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "%", "%%", "$", "$$").Replace(s)
	return "\"" + s + "\"", nil
}
