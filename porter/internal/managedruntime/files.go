package managedruntime

import (
	"context"
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

func (c Controller) directories(parts ...string) error {
	if !filepath.IsAbs(c.Home) || filepath.Clean(c.Home) != c.Home {
		return ErrDefinition
	}
	current := c.Home
	for _, part := range append([]string{""}, parts...) {
		if part != "" {
			current = filepath.Join(current, part)
		}
		if err := os.Mkdir(current, 0700); err != nil && !os.IsExist(err) {
			return ErrState
		}
		st, err := os.Lstat(current)
		if err != nil || !st.IsDir() || st.Mode().Perm()&0022 != 0 {
			return ErrState
		}
		owner, ok := st.Sys().(*syscall.Stat_t)
		if !ok || owner.Uid != uint32(os.Getuid()) {
			return ErrState
		}
	}
	return nil
}

func (c Controller) lock(d Definition) (func(), error) {
	if !validDefinition(d) {
		return nil, ErrDefinition
	}
	if err := c.directories(".local", "state", "valise", "managed-runtime"); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(c.Home, ".local/state/valise/managed-runtime", d.name+".lock"), os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, ErrState
	}
	st, err := f.Stat()
	owner, ok := stSys(st)
	if err != nil || !ok || !st.Mode().IsRegular() || owner.Nlink != 1 || owner.Uid != uint32(os.Getuid()) || st.Mode().Perm()&0077 != 0 {
		f.Close()
		return nil, ErrState
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, ErrState
	}
	return func() { f.Close() }, nil
}

func stSys(st os.FileInfo) (*syscall.Stat_t, bool) {
	if st == nil {
		return nil, false
	}
	s, ok := st.Sys().(*syscall.Stat_t)
	return s, ok
}

func (c Controller) checkFile(d Definition, expected string) error {
	if !validDefinition(d) {
		return ErrDefinition
	}
	f, err := os.OpenFile(c.path(d), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(err, os.ErrNotExist) && expected == "" {
		return nil
	}
	if err != nil {
		return ErrDefinition
	}
	defer f.Close()
	st, err := f.Stat()
	owner, ok := stSys(st)
	if err != nil || !ok || !st.Mode().IsRegular() || owner.Nlink != 1 || owner.Uid != uint32(os.Getuid()) || st.Mode().Perm()&0077 != 0 || st.Size() > 1<<20 {
		return ErrDefinition
	}
	b, err := io.ReadAll(io.LimitReader(f, 1<<20+1))
	if err != nil || digest(string(b)) != expected {
		return ErrDefinition
	}
	return nil
}

// RequireAbsent checks the exact future managed entry without creating it.
// First activation must not replace a foreign or previously loaded unit.
func (c Controller) RequireAbsent(ctx context.Context, d Definition) error {
	if !validDefinition(d) {
		return ErrDefinition
	}
	return c.requireNameAbsent(ctx, d.name)
}

// RequireAgentAbsent also works before any programme has been built. It only
// observes the one fixed future entry; it cannot accept another unit's name.
func (c Controller) RequireAgentAbsent(ctx context.Context, agent identity.AgentRef) error {
	if protection.ValidateAgent(agent) != nil {
		return ErrDefinition
	}
	return c.requireNameAbsent(ctx, "valise-agent-"+environment.Key(agent)+".service")
}

func (c Controller) requireNameAbsent(ctx context.Context, name string) error {
	if !filepath.IsAbs(c.Home) || filepath.Clean(c.Home) != c.Home {
		return ErrDefinition
	}
	current := c.Home
	for _, part := range []string{"", ".config", "systemd", "user"} {
		if part != "" {
			current = filepath.Join(current, part)
		}
		st, err := os.Lstat(current)
		if os.IsNotExist(err) {
			break
		}
		owner, ok := stSys(st)
		if err != nil || !ok || !st.IsDir() || owner.Uid != uint32(os.Getuid()) || st.Mode().Perm()&0022 != 0 {
			return ErrDefinition
		}
	}
	d := Definition{name: name}
	if _, err := os.Lstat(c.path(d)); !os.IsNotExist(err) {
		return ErrDefinition
	}
	p, err := c.properties(ctx, d)
	if err != nil || p["LoadState"] != "not-found" || p["MainPID"] != "0" || p["FragmentPath"] != "" || p["DropInPaths"] != "" || p["ControlGroup"] != "" {
		return ErrState
	}
	if job, ok := p["Job"]; !ok || job != "" && job != "0" {
		return ErrState
	}
	return nil
}

// Load reconstructs only a definition whose exact digest was recorded by the
// activation controller. It is read-only: reopening a task cannot install,
// reload, start, stop or enable anything. The digest is evidence, not authority;
// callers still enforce the human/admin boundary for every subsequent action.
func (c Controller) Load(agent identity.AgentRef, expected string) (Definition, error) {
	if protection.ValidateAgent(agent) != nil || len(expected) != 64 {
		return Definition{}, ErrDefinition
	}
	d := Definition{name: "valise-agent-" + environment.Key(agent) + ".service"}
	f, err := os.OpenFile(c.path(d), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return Definition{}, ErrDefinition
	}
	defer f.Close()
	st, err := f.Stat()
	owner, ok := stSys(st)
	if err != nil || !ok || !st.Mode().IsRegular() || owner.Nlink != 1 || owner.Uid != uint32(os.Getuid()) || st.Mode().Perm()&0077 != 0 || st.Size() > 1<<20 {
		return Definition{}, ErrDefinition
	}
	b, err := io.ReadAll(io.LimitReader(f, 1<<20+1))
	if err != nil {
		return Definition{}, ErrDefinition
	}
	d.content, d.digest = string(b), digest(string(b))
	if d.digest != expected || !validDefinition(d) {
		return Definition{}, ErrDefinition
	}
	return d, nil
}

// Install is explicitly called after a human plan and a durable stop intent.
// It never starts or enables the service. Existing/foreign definitions cannot
// be overwritten just because their filename resembles an Agent's name.
func (c Controller) Install(ctx context.Context, d Definition, expected string) error {
	unlock, err := c.lock(d)
	if err != nil {
		return err
	}
	defer unlock()
	if err := c.checkFile(d, expected); err != nil {
		return err
	}
	p, err := c.properties(ctx, d)
	if err != nil {
		return err
	}
	if job, ok := p["Job"]; !ok || job != "" && job != "0" {
		return ErrState
	}
	if expected == "" {
		if p["LoadState"] != "not-found" {
			return ErrState
		}
	} else {
		old := d
		old.digest = expected
		ob, err := classifyProperties(p, old, c.path(d))
		if err != nil || ob.PendingJob || ob.MainPID != 0 || ob.ActiveState != "inactive" && ob.ActiveState != "failed" {
			return ErrState
		}
		empty, err := emptyGroup(ob.ControlGroup, d.name)
		if err != nil || !empty {
			return ErrState
		}
	}
	if err := c.directories(".config", "systemd", "user"); err != nil {
		return err
	}
	dir := filepath.Dir(c.path(d))
	f, err := os.CreateTemp(dir, ".valise-unit-*")
	if err != nil {
		return ErrState
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.WriteString(d.content)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return ErrState
	}
	if err := c.checkFile(d, expected); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), c.path(d)); err != nil {
		return ErrState
	}
	parent, err := os.Open(dir)
	if err != nil {
		return ErrState
	}
	err = parent.Sync()
	parent.Close()
	if err != nil {
		return ErrState
	}
	_, code, err := c.call(ctx, "daemon-reload")
	if err != nil || code != 0 {
		return ErrAction
	}
	return nil
}

// Remove only removes this exact, stopped definition after explicit cleanup.
// Startup links must be removed by the activation transaction beforehand.
func (c Controller) Remove(ctx context.Context, d Definition) error {
	unlock, err := c.lock(d)
	if err != nil {
		return err
	}
	defer unlock()
	ob, err := c.Read(ctx, d)
	if err != nil || !ob.Stopped {
		return ErrState
	}
	p, err := c.properties(ctx, d)
	if err != nil || strings.Contains(p["UnitFileState"], "enabled") {
		return ErrState
	}
	if err := os.Remove(c.path(d)); err != nil {
		return ErrState
	}
	parent, err := os.Open(filepath.Dir(c.path(d)))
	if err != nil {
		return ErrState
	}
	err = parent.Sync()
	parent.Close()
	if err != nil {
		return ErrState
	}
	_, code, err := c.call(ctx, "daemon-reload")
	if err != nil || code != 0 {
		return ErrAction
	}
	return nil
}
