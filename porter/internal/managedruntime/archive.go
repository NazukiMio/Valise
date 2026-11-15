package managedruntime

import (
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"valise/core/environment"
	"valise/core/identity"
	"valise/core/protection"
)

func validDigest(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == 32 && strings.ToLower(s) == s
}

func (c Controller) definitionDir(name string) string {
	return filepath.Join(c.Home, ".local/state/valise/managed-runtime/definitions", name)
}

// Remember saves a compiler-produced definition, not a request's unit text.
// The immutable, owned archive permits rollback after the installed unit has
// changed. No service action, startup change or deletion happens here.
func (c Controller) Remember(d Definition) error {
	unlock, err := c.lock(d)
	if err != nil {
		return err
	}
	defer unlock()
	if err := c.directories(".local", "state", "valise", "managed-runtime", "definitions", d.name); err != nil {
		return err
	}
	path := filepath.Join(c.definitionDir(d.name), d.digest+".service")
	if _, err := readDefinitionAt(path, d.name, d.digest); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.CreateTemp(c.definitionDir(d.name), ".definition-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := f.Chmod(0600); err != nil {
		return err
	}
	if _, err := f.WriteString(d.content); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(c.definitionDir(d.name))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

// Remembered is read-only and requires the exact digest of an already trusted
// transaction. It cannot choose arbitrary paths, install or replay anything.
func (c Controller) Remembered(agent identity.AgentRef, expected string) (Definition, error) {
	if protection.ValidateAgent(agent) != nil || !validDigest(expected) || !filepath.IsAbs(c.Home) || filepath.Clean(c.Home) != c.Home {
		return Definition{}, ErrDefinition
	}
	name := "valise-agent-" + environment.Key(agent) + ".service"
	current := c.Home
	for _, part := range []string{"", ".local", "state", "valise", "managed-runtime", "definitions", name} {
		if part != "" {
			current = filepath.Join(current, part)
		}
		st, err := os.Lstat(current)
		owner, ok := stSys(st)
		if err != nil {
			return Definition{}, err
		}
		if !ok || !st.IsDir() || owner.Uid != uint32(os.Getuid()) || st.Mode().Perm()&0022 != 0 {
			return Definition{}, ErrDefinition
		}
	}
	return readDefinitionAt(filepath.Join(current, expected+".service"), name, expected)
}

func readDefinitionAt(path, name, expected string) (Definition, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return Definition{}, err
	}
	defer f.Close()
	st, err := f.Stat()
	owner, ok := stSys(st)
	if err != nil || !ok || !st.Mode().IsRegular() || owner.Uid != uint32(os.Getuid()) || owner.Nlink != 1 || st.Mode().Perm()&0077 != 0 || st.Size() > 1<<20 {
		return Definition{}, ErrDefinition
	}
	b, err := io.ReadAll(io.LimitReader(f, 1<<20+1))
	d := Definition{name, string(b), expected}
	if err != nil || !validDefinition(d) {
		return Definition{}, ErrDefinition
	}
	return d, nil
}
