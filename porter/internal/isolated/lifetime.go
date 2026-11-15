package isolated

import (
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

// lifetimeFile is private controller metadata, never a harness-visible file.
// Closing a parent copy must NOT LOCK_UN: bwrap's namespace init inherits the
// same open file description until its sandbox lifetime ends.
const lifetimeFile = "lifetime.lock"

func lifetime(path string, create bool) (*os.File, error) {
	flags := os.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_NONBLOCK
	if create {
		flags |= os.O_CREATE | os.O_EXCL | os.O_RDWR
	}
	f, err := os.OpenFile(path, flags, 0600)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !ok || !st.Mode().IsRegular() || owner.Uid != uint32(os.Getuid()) || owner.Nlink != 1 || st.Mode().Perm()&0077 != 0 || st.Size() != 0 {
		f.Close()
		return nil, ErrWorkspace
	}
	return f, nil
}

func probeLifetime(w *Workspace) (*os.File, error) {
	f, err := lifetime(filepath.Join(w.dir, "control", lifetimeFile), true)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_SH|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return nil, err
	}
	dir, err := os.Open(filepath.Join(w.dir, "control"))
	if err != nil {
		f.Close()
		return nil, err
	}
	err = dir.Sync()
	dir.Close()
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// HoldProbeStopped is used only by a trusted explicit cleanup controller.
// It never creates or repairs metadata. The caller validates the fixed parent
// directory and holds this handle throughout removal; nil means no probe was
// launched in this workspace, not that any old untracked probe is stopped.
func HoldProbeStopped(workspace string) (*os.File, error) {
	control := filepath.Join(workspace, "control")
	st, err := os.Lstat(control)
	if err != nil {
		return nil, err
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !ok || !st.IsDir() || owner.Uid != uint32(os.Getuid()) || st.Mode().Perm()&0077 != 0 {
		return nil, ErrWorkspace
	}
	f, err := lifetime(filepath.Join(control, lifetimeFile), false)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
