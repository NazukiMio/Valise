// Package forced parses the command a client sent to a forced-command key
// (authorized_keys command="valise-porter serve --tier daily"). sshd puts
// the client's command in SSH_ORIGINAL_COMMAND and runs ours instead; it is
// parsed here, never handed to a shell.
package forced

import (
	"errors"
	"path"
	"strings"
)

// DailySubcommands are the porter subcommands the daily key may run
// (docs/ARCHITECTURE.md 2.1).
var DailySubcommands = map[string]bool{"rpc": true, "version": true, "vault-cat": true}

// ErrNotAllowed is returned for anything the daily key may not run.
var ErrNotAllowed = errors.New("这把钥匙只能运行 valise-porter 的日常操作")

// Split splits a POSIX shell command line into words, supporting single
// quotes, double quotes and backslash escapes. Expansions, globs and
// operators are not interpreted; "$HOME" stays literal.
func Split(s string) ([]string, error) {
	var words []string
	var b strings.Builder
	inWord := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n':
			if inWord {
				words = append(words, b.String())
				b.Reset()
				inWord = false
			}
		case c == '\'':
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				return nil, errors.New("unterminated quote")
			}
			b.WriteString(s[i+1 : i+1+j])
			i += j + 1
			inWord = true
		case c == '"':
			i++
			for ; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' && i+1 < len(s) && strings.IndexByte(`"\$`+"`", s[i+1]) >= 0 {
					i++
				}
				b.WriteByte(s[i])
			}
			if i >= len(s) {
				return nil, errors.New("unterminated quote")
			}
			inWord = true
		case c == '\\':
			if i+1 < len(s) {
				i++
				b.WriteByte(s[i])
			}
			inWord = true
		case strings.IndexByte(";&|<>()`", c) >= 0:
			return nil, ErrNotAllowed
		default:
			b.WriteByte(c)
			inWord = true
		}
	}
	if inWord {
		words = append(words, b.String())
	}
	return words, nil
}

// Daily returns the porter arguments for a daily-key command line: the
// first word must name valise-porter, the second a daily subcommand.
func Daily(cmdline string) ([]string, error) {
	words, err := Split(cmdline)
	if err != nil {
		return nil, ErrNotAllowed
	}
	if len(words) < 2 || path.Base(words[0]) != "valise-porter" {
		return nil, ErrNotAllowed
	}
	if _, granted := GrantSubcommands[words[1]]; granted {
		if _, _, ok := TakeGrant(words[1:]); !ok {
			return nil, ErrNotAllowed
		}
	} else if !DailySubcommands[words[1]] {
		return nil, ErrNotAllowed
	}
	return words[1:], nil
}
