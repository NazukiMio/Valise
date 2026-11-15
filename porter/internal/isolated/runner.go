// Package isolated runs finite prepared tasks without host home, session
// sockets or network access. This is an internal primitive, not an RPC command
// runner or proof that any harness supports parallel restoration.
package isolated

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
)

const Home = "/home/valise"

var (
	ErrUnsupported = errors.New("isolated environment unavailable")
	ErrWorkspace   = errors.New("isolated workspace is invalid")
	ErrTask        = errors.New("isolated task failed; child output was not collected")
	ErrDeadline    = errors.New("isolated task requires a deadline")
)

// Spec is constructed only from a validated signed profile by a trusted
// controller. No arbitrary mount, inherited environment or network option is
// provided. The first module deliberately has no long-running service mode.
type Spec struct {
	Home       string
	Argv       []string
	WorkingDir string
	Path       []string
}

// Run discards child stdout/stderr: harness output can contain credentials.
// Cancellation kills bwrap and its private PID namespace, not the active Agent.
func Run(ctx context.Context, w *Workspace, spec Spec) error {
	cmd, err := command(ctx, w, spec)
	if err != nil {
		return err
	}
	// All standard streams stay disconnected. No command content or child
	// output is included in returned errors, audit records or logs.
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrTask
	}
	return nil
}

func command(ctx context.Context, w *Workspace, spec Spec) (*exec.Cmd, error) {
	if ctx == nil {
		return nil, ErrDeadline
	}
	if _, ok := ctx.Deadline(); !ok {
		return nil, ErrDeadline
	}
	if runtime.GOOS != "linux" {
		return nil, ErrUnsupported
	}
	// Resolve only the known OS location, never a restored PATH executable.
	bwrap := "/usr/bin/bwrap"
	if st, err := os.Stat(bwrap); err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0o022 != 0 {
		return nil, ErrUnsupported
	}
	args, err := arguments(w, spec)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, bwrap, args...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C", "HOME=/nonexistent"}
	cmd.Dir = "/"
	return cmd, nil
}

func arguments(w *Workspace, s Spec) ([]string, error) {
	home := s.home()
	if w == nil || w.dir == "" || home != w.GuestHome() || len(s.Argv) == 0 || s.Argv[0] == "" || !privatePathAt(home, s.WorkingDir) {
		return nil, ErrWorkspace
	}
	for _, a := range s.Argv {
		if strings.ContainsRune(a, 0) {
			return nil, ErrWorkspace
		}
	}
	for _, d := range s.Path {
		if !privatePathAt(home, d) {
			return nil, ErrWorkspace
		}
	}
	if err := validateWorkspace(w); err != nil {
		return nil, err
	}
	args := []string{"--unshare-all", "--unshare-user", "--disable-userns", "--die-with-parent", "--new-session", "--cap-drop", "ALL", "--clearenv"}
	// Empty root is bwrap's default. Never bind / or the host's /home, /etc,
	// /tmp or /run. System program directories are read-only.
	for _, dir := range []string{"/usr/bin", "/usr/sbin", "/usr/lib", "/usr/lib64"} {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}
		if err := publicPrograms(dir); err != nil {
			return nil, err
		}
		args = append(args, "--ro-bind", dir, dir)
	}
	for _, d := range []string{"bin", "sbin", "lib", "lib64"} {
		p := "/" + d
		st, err := os.Lstat(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, ErrUnsupported
		}
		if st.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(p)
			if err != nil || (link != "usr/"+d && link != "/usr/"+d) {
				return nil, ErrUnsupported
			}
			args = append(args, "--symlink", "usr/"+d, p)
		} else {
			if err := publicPrograms(p); err != nil {
				return nil, err
			}
			args = append(args, "--ro-bind", p, p)
		}
	}
	args = append(args, "--ro-bind", filepath.Join(w.dir, "etc"), "/etc", "--bind", w.HomeDir(), home)
	for _, rel := range w.programs {
		args = append(args, "--ro-bind", filepath.Join(w.ProgramDir(), filepath.FromSlash(rel)), home+"/"+rel)
	}
	args = append(args,
		"--proc", "/proc", "--dev", "/dev", "--tmpfs", "/tmp", "--tmpfs", "/run",
		"--remount-ro", "/",
		"--setenv", "HOME", home, "--setenv", "PATH", strings.Join(append(append([]string{}, s.Path...), "/usr/bin", "/bin"), ":"),
		"--setenv", "LANG", "C", "--setenv", "TMPDIR", "/tmp", "--chdir", s.WorkingDir, "--")
	return append(args, s.Argv...), nil
}

// A read-only mount still permits connecting to a Unix socket. Only map known
// OS program directories, excluding /usr/local and /usr/share, and reject any
// special node in them. Inspect metadata only, never file contents.
func publicPrograms(dir string) error {
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil || (resolved != dir && !slices.Contains([]string{"/usr/bin", "/usr/sbin", "/usr/lib", "/usr/lib64"}, resolved) && !strings.HasPrefix(resolved, "/usr/lib/")) {
		return ErrUnsupported
	}
	return filepath.WalkDir(resolved, func(_ string, e fs.DirEntry, err error) error {
		if err != nil {
			return ErrUnsupported
		}
		st, err := e.Info()
		if err != nil || st.Mode()&(os.ModeSocket|os.ModeDevice|os.ModeNamedPipe) != 0 {
			return ErrUnsupported
		}
		return nil
	})
}

func validateWorkspace(w *Workspace) error {
	for _, d := range []string{w.dir, w.HomeDir(), w.ProgramDir(), filepath.Join(w.dir, "etc")} {
		st, err := os.Lstat(d)
		if err != nil || !st.IsDir() || st.Mode().Perm()&0o077 != 0 {
			return ErrWorkspace
		}
		owner, ok := st.Sys().(*syscall.Stat_t)
		if !ok || owner.Uid != uint32(os.Getuid()) {
			return ErrWorkspace
		}
	}
	if err := w.validateProgramMounts(); err != nil {
		return err
	}
	// Never expose a host socket, device, FIFO or a regular file hardlinked
	// to the active environment. Symlinks resolve inside the private root.
	err := filepath.WalkDir(w.dir, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return ErrWorkspace
		}
		st, err := e.Info()
		if err != nil || st.Mode()&(os.ModeSocket|os.ModeDevice|os.ModeNamedPipe) != 0 {
			return ErrWorkspace
		}
		if stat, ok := st.Sys().(*syscall.Stat_t); ok && st.Mode().IsRegular() && stat.Nlink != 1 {
			return ErrWorkspace
		}
		if owner, ok := st.Sys().(*syscall.Stat_t); !ok || owner.Uid != uint32(os.Getuid()) {
			return ErrWorkspace
		}
		return nil
	})
	if err != nil {
		return ErrWorkspace
	}
	return nil
}
