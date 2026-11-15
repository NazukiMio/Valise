// Package environments stores private runtime environment records. It is not
// exposed for policy settings; runtime execution remains an internal adapter.
package environments

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
	"valise/core/environment"
	"valise/core/identity"
)

var ErrConflict = errors.New("environment registry revision conflict")

type Store struct{ Home string }

func (s Store) dir() string {
	return filepath.Join(s.Home, ".local", "state", "valise", "environments")
}
func (s Store) path(a identity.AgentRef) string {
	return filepath.Join(s.dir(), environment.Key(a)+".json")
}

// Load never initializes or reconciles a record and never executes a task.
func (s Store) Load(a identity.AgentRef) (environment.Registry, error) {
	var r environment.Registry
	if err := s.directories(false); err != nil {
		return r, err
	}
	f, err := privateFile(s.path(a), false)
	if err != nil {
		return r, err
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	if err != nil {
		return r, err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(&r); err != nil {
		return r, err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return r, environment.ErrState
	}
	if r.Agent != a {
		return r, environment.ErrStale
	}
	return r, r.Validate()
}

// Update serializes CAS writes across porter processes. For creation expected
// is zero; fn must return a validated initial record with revision 1. fn may
// only compute state: it must not touch services, files or the network.
func (s Store) Update(a identity.AgentRef, expected uint64, fn func(environment.Registry) (environment.Registry, error)) (environment.Registry, error) {
	var zero environment.Registry
	unlock, err := s.lock(a)
	if err != nil {
		return zero, err
	}
	defer unlock()
	current, err := s.Load(a)
	if errors.Is(err, os.ErrNotExist) && expected == 0 {
		current, err = zero, nil
	}
	if err != nil {
		return zero, err
	}
	if current.Revision != expected || expected == ^uint64(0) {
		return current, ErrConflict
	}
	next, err := fn(current)
	if err != nil {
		return current, err
	}
	if expected == 0 {
		policy, err := s.policy(a)
		if err != nil {
			return current, err
		}
		next.Mode = policy.Mode
	}
	if next.Agent != a || next.Revision != expected+1 || next.Validate() != nil {
		return current, environment.ErrState
	}
	b, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return current, err
	}
	if err := s.save(a, b); err != nil {
		return current, err
	}
	return next, nil
}

func (s Store) directories(create bool) error {
	current := s.Home
	if err := ownedDirectory(current); err != nil {
		return err
	}
	for _, part := range []string{".local", "state", "valise", "environments"} {
		current = filepath.Join(current, part)
		if create {
			if err := os.Mkdir(current, 0700); err != nil && !errors.Is(err, os.ErrExist) {
				return err
			}
		}
		if err := ownedDirectory(current); err != nil {
			return err
		}
	}
	if st, err := os.Lstat(current); err != nil || st.Mode().Perm()&0077 != 0 {
		return environment.ErrState
	}
	return nil
}

func (s Store) lock(a identity.AgentRef) (func(), error) {
	if err := s.directories(true); err != nil {
		return nil, err
	}
	f, err := privateFile(filepath.Join(s.dir(), environment.Key(a)+".lock"), true)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() { unix.Flock(int(f.Fd()), unix.LOCK_UN); f.Close() }, nil
}

func privateFile(path string, create bool) (*os.File, error) {
	flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
	if create {
		flags |= unix.O_CREAT | unix.O_RDWR
	}
	fd, err := unix.Open(path, flags, 0o600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !st.Mode().IsRegular() || st.Mode().Perm()&0o077 != 0 || !ok || owner.Uid != uint32(os.Getuid()) || owner.Nlink != 1 {
		f.Close()
		return nil, environment.ErrState
	}
	return f, nil
}

func (s Store) save(a identity.AgentRef, b []byte) error {
	return s.savePath(s.path(a), b)
}

func (s Store) savePath(path string, b []byte) error {
	f, err := os.CreateTemp(s.dir(), ".registry-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := f.Chmod(0o600); err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
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
	d, err := os.Open(s.dir())
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
