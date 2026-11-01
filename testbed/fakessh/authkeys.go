package main

import (
	"os"
	"path/filepath"
	"strings"
)

// noPubkey reports -o PubkeyAuthentication=no (a password login, which the
// fake host always accepts).
func (inv invocation) noPubkey() bool {
	for _, o := range inv.options {
		if strings.EqualFold(strings.ReplaceAll(o, " ", "="), "PubkeyAuthentication=no") {
			return true
		}
	}
	return false
}

// authNone reports -o PreferredAuthentications=none: a login with no
// credential, which the fake host refuses unless FAKESSH_AUTH_NONE lists
// the alias (a gateway that lets anyone in).
func (inv invocation) authNone() bool {
	for _, o := range inv.options {
		if strings.EqualFold(strings.ReplaceAll(o, " ", "="), "PreferredAuthentications=none") {
			return true
		}
	}
	return false
}

// authorize emulates sshd's public-key check against the fake home's
// ~/.ssh/authorized_keys: the key in <identity>.pub must be listed. It
// returns the line's command="…" option, if any.
func authorize(home, identity string) (forced string, ok bool) {
	pub, err := os.ReadFile(identity + ".pub")
	if err != nil {
		return "", false
	}
	f := strings.Fields(string(pub))
	if len(f) < 2 {
		return "", false
	}
	b, err := os.ReadFile(filepath.Join(home, ".ssh", "authorized_keys"))
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(b), "\n") {
		opts, key := splitOptions(strings.TrimSpace(line))
		k := strings.Fields(key)
		if len(k) >= 2 && k[0] == f[0] && k[1] == f[1] {
			return commandOption(opts), true
		}
	}
	return "", false
}

// splitOptions separates the leading options field of an authorized_keys
// line from the key, honoring double quotes.
func splitOptions(line string) (opts, key string) {
	if strings.HasPrefix(line, "ssh-") || strings.HasPrefix(line, "ecdsa-") || strings.HasPrefix(line, "sk-") {
		return "", line
	}
	quoted := false
	for i, c := range line {
		switch {
		case c == '"':
			quoted = !quoted
		case c == ' ' && !quoted:
			return line[:i], strings.TrimSpace(line[i:])
		}
	}
	return line, ""
}

// commandOption returns the value of command="…" in an options field.
func commandOption(opts string) string {
	i := strings.Index(opts, `command="`)
	if i < 0 {
		return ""
	}
	rest := opts[i+len(`command="`):]
	var b strings.Builder
	for j := 0; j < len(rest); j++ {
		if rest[j] == '\\' && j+1 < len(rest) && rest[j+1] == '"' {
			b.WriteByte('"')
			j++
			continue
		}
		if rest[j] == '"' {
			break
		}
		b.WriteByte(rest[j])
	}
	return b.String()
}
