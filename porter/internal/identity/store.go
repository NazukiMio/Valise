// Package identity persists accepted identities without changing dossiers or vaults.
package identity

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"

	"valise/core/enroll"
	model "valise/core/identity"
	"valise/core/valise"
)

func Dir(home string) string           { return filepath.Join(home, ".config", "valise", "identity") }
func agentPath(home, id string) string { return filepath.Join(Dir(home), "agents", id+".json") }

func privateDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return errors.New("identity directory is not a real directory")
	}
	return os.Chmod(path, 0700)
}

// WithLock serializes accepted enrollment and its dossier writes across processes.
func WithLock(home string, fn func() error) error {
	if err := privateDir(Dir(home)); err != nil {
		return err
	}
	if err := privateDir(filepath.Join(Dir(home), "agents")); err != nil {
		return err
	}
	return locked(home, true, fn)
}

func locked(home string, create bool, fn func() error) error {
	if fi, err := os.Lstat(Dir(home)); err == nil {
		if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
			return errors.New("identity directory is not a real directory")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	flags := os.O_RDWR
	if create {
		flags |= os.O_CREATE
	}
	f, err := os.OpenFile(filepath.Join(Dir(home), "lock"), flags|syscall.O_NOFOLLOW, 0600)
	if errors.Is(err, os.ErrNotExist) && !create {
		// No identity has been accepted; reading does not initialize anything.
		if _, e := os.Lstat(Dir(home)); errors.Is(e, os.ErrNotExist) {
			return fn()
		}
	}
	if err != nil {
		return err
	}
	defer f.Close()
	mode := syscall.LOCK_SH
	if create {
		mode = syscall.LOCK_EX
	}
	if err := syscall.Flock(int(f.Fd()), mode); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	if create {
		if err := f.Chmod(0600); err != nil {
			return err
		}
	}
	return fn()
}

func readJSON(path string, v any) (bool, error) {
	if fi, err := os.Lstat(filepath.Dir(path)); err == nil {
		if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
			return false, errors.New("identity parent is not a real directory")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return false, err
	}
	if !fi.Mode().IsRegular() {
		return false, errors.New("identity file is not regular")
	}
	if err := json.NewDecoder(f).Decode(v); err != nil {
		return false, err
	}
	return true, nil
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".identity-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = f.Chmod(0600); err != nil {
		return err
	}
	if _, err = f.Write(append(b, '\n')); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func reconciliation() error {
	return valise.New("identity_needs_reconciliation", "上次接入提交尚未完成，身份需要核实", "核对实例档案与接入提交记录后再继续")
}

func server(home string) (*model.Server, error) {
	var s model.Server
	ok, err := readJSON(filepath.Join(Dir(home), "server.json"), &s)
	if err != nil || !ok {
		return nil, err
	}
	if s.Schema != model.Schema || s.ServerUID == "" || s.CreatedAt.IsZero() {
		return nil, errors.New("invalid server identity")
	}
	return &s, nil
}

// ReadServer reads the accepted server identity; nil means not yet bound.
func ReadServer(home string) (s *model.Server, err error) {
	err = locked(home, false, func() error {
		if err := checkCommit(home); err != nil {
			return err
		}
		var e error
		s, e = server(home)
		return e
	})
	return
}

// Read never initializes identity, and never exposes a partially committed generation.
func Read(home, id string) (b model.Binding, err error) {
	b.IdentityState = model.LegacyUnbound
	if err = enroll.ValidInstanceID(id); err != nil {
		return b, err
	}
	err = locked(home, false, func() error {
		if err := checkCommit(home); err != nil {
			b.IdentityState = model.NeedsReconciliation
			return err
		}
		var a model.Agent
		ok, err := readJSON(agentPath(home, id), &a)
		if err != nil || !ok {
			return err
		}
		s, err := server(home)
		if err != nil {
			return err
		}
		if s == nil || a.Schema != model.Schema || a.Ref.ServerUID != s.ServerUID || a.Ref.InstanceID != id || a.Ref.InstallID == "" || a.Ref.Generation == "" || a.CreatedAt.IsZero() {
			return errors.New("invalid agent identity")
		}
		b.IdentityState, b.Agent = model.Bound, &a
		return nil
	})
	return
}
