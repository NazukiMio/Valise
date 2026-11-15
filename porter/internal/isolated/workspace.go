package isolated

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Workspace always owns fresh data and program trees. GuestHome preserves an
// authenticated absolute prefix without mounting the host's corresponding HOME.
type Workspace struct {
	dir      string
	home     string
	programs []string
}

func NewWorkspace(parent string) (*Workspace, error) { return NewWorkspaceForHome(parent, Home) }

func NewWorkspaceForHome(parent, home string) (*Workspace, error) {
	if !ValidHome(home) {
		return nil, ErrWorkspace
	}
	dir, err := os.MkdirTemp(parent, "valise-isolated-")
	if err != nil {
		return nil, err
	}
	w := &Workspace{dir: dir, home: home}
	for _, p := range []string{w.HomeDir(), w.ProgramDir(), filepath.Join(dir, "etc"), filepath.Join(dir, "control")} {
		if err := os.Mkdir(p, 0o700); err != nil {
			return nil, err
		}
	}
	files := map[string]string{
		"passwd": fmt.Sprintf("valise:x:%d:%d:Valise:%s:/bin/false\n", os.Getuid(), os.Getgid(), home),
		"group":  fmt.Sprintf("valise:x:%d:\n", os.Getgid()),
		"hosts":  "127.0.0.1 localhost\n::1 localhost\n",
	}
	for p, data := range files {
		if err := os.WriteFile(filepath.Join(dir, "etc", p), []byte(data), 0o600); err != nil {
			return nil, err
		}
	}
	return w, nil
}

func (w *Workspace) HomeDir() string    { return filepath.Join(w.dir, "home") }
func (w *Workspace) ProgramDir() string { return filepath.Join(w.dir, "programs") }
func (w *Workspace) GuestHome() string  { return w.home }

// OpenWorkspace reconstructs an existing controller-owned workspace without
// creating directories or running anything. Its caller binds dir to a fixed
// private Agent/slot revision; no RPC accepts dir or a Workspace value.
func OpenWorkspace(dir, home string, paths []string) (*Workspace, error) {
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir || !ValidHome(home) || len(paths) == 0 {
		return nil, ErrWorkspace
	}
	w := &Workspace{dir: dir, home: home, programs: append([]string{}, paths...)}
	for i, rel := range paths {
		if rel == "" || !privatePathAt(home, home+"/"+rel) {
			return nil, ErrWorkspace
		}
		for _, other := range paths[:i] {
			if overlap(rel, other) {
				return nil, ErrWorkspace
			}
		}
	}
	if err := validateWorkspace(w); err != nil {
		return nil, err
	}
	return w, nil
}

// SetProgramPaths accepts only non-overlapping directories under GuestHome,
// never an external source/target mount. Call once, before writing materials.
func (w *Workspace) SetProgramPaths(rels []string) error {
	if w == nil || w.programs != nil || len(rels) == 0 {
		return ErrWorkspace
	}
	for i, rel := range rels {
		if !privatePathAt(w.home, w.home+"/"+rel) || rel == "" {
			return ErrWorkspace
		}
		for _, other := range rels[:i] {
			if overlap(rel, other) {
				return ErrWorkspace
			}
		}
	}
	for _, rel := range rels {
		for _, base := range []string{w.HomeDir(), w.ProgramDir()} {
			r, err := os.OpenRoot(base)
			if err != nil {
				return ErrWorkspace
			}
			err = r.MkdirAll(rel, 0o700)
			r.Close()
			if err != nil {
				return ErrWorkspace
			}
		}
	}
	w.programs = append([]string{}, rels...)
	return nil
}

func (w *Workspace) validateProgramMounts() error {
	for _, base := range []string{w.HomeDir(), w.ProgramDir()} {
		r, err := os.OpenRoot(base)
		if err != nil {
			return ErrWorkspace
		}
		for _, rel := range w.programs {
			for part := rel; part != "."; part = path.Dir(part) {
				st, err := r.Lstat(part)
				if err != nil || !st.IsDir() || st.Mode().Perm()&0o077 != 0 {
					r.Close()
					return ErrWorkspace
				}
			}
		}
		r.Close()
	}
	return nil
}

func (s Spec) home() string {
	if s.Home == "" {
		return Home
	}
	return s.Home
}

func privatePathAt(home, p string) bool {
	return ValidHome(home) && (p == home || strings.HasPrefix(p, home+"/")) && path.Clean(p) == p && !strings.ContainsAny(p, "\\\x00\r\n:")
}

func overlap(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

// ValidHome excludes roots that could shadow the OS, controller or private
// pseudo-filesystems, including their ancestors. It does not inspect a host HOME.
func ValidHome(home string) bool {
	if !path.IsAbs(home) || path.Clean(home) != home || home == "/" || strings.ContainsAny(home, "\\\x00\r\n:") {
		return false
	}
	for _, reserved := range []string{"/usr", "/bin", "/sbin", "/lib", "/lib64", "/etc", "/proc", "/sys", "/dev", "/tmp", "/run", "/valise"} {
		if overlap(home, reserved) {
			return false
		}
	}
	return true
}
