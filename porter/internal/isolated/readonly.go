package isolated

import (
	"os"
	"path/filepath"
	"strings"
)

// ReadonlyAttachedArguments is the fixed policy for a daily finite check.
// It shares the active network, but mounts actual data and programmes read-only
// and replaces the persistent workspace HOME with a private ephemeral tmpfs.
func ReadonlyAttachedArguments(w *Workspace, s Spec, data []DataBinding) ([]string, error) {
	args, err := managedArguments(w, s, data, false)
	if err != nil {
		return nil, err
	}
	// bwrap must not create a programme mount point inside an active root.
	// These directories already exist in a stable running environment; if
	// removed/replaced, a query reports unknown rather than repairing them.
	for _, rel := range w.programs {
		guest := s.home() + "/" + rel
		for _, root := range data {
			if !strings.HasPrefix(guest, root.Guest+"/") {
				continue
			}
			host := filepath.Join(root.Host, filepath.FromSlash(strings.TrimPrefix(guest, root.Guest+"/")))
			real, err := filepath.EvalSymlinks(host)
			info, statErr := os.Lstat(host)
			if err != nil || statErr != nil || real != host || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return nil, ErrWorkspace
			}
		}
	}
	var out []string
	home, roots := false, 0
	for i := 0; i < len(args); {
		switch args[i] {
		case "--":
			out = append(out, args[i:]...)
			i = len(args)
		case "--setenv":
			if i+2 >= len(args) {
				return nil, ErrWorkspace
			}
			out = append(out, args[i:i+3]...)
			i += 3
		case "--bind":
			if i+2 >= len(args) {
				return nil, ErrWorkspace
			}
			if args[i+1] == w.HomeDir() && args[i+2] == s.home() {
				out = append(out, "--perms", "0700", "--tmpfs", s.home())
				home = true
			} else {
				found := false
				for _, root := range data {
					if args[i+1] == root.Host && args[i+2] == root.Guest {
						found = true
					}
				}
				if !found {
					return nil, ErrWorkspace
				}
				out = append(out, "--ro-bind", args[i+1], args[i+2])
				roots++
			}
			i += 3
		default:
			out = append(out, args[i])
			i++
		}
	}
	if !home || roots != len(data) {
		return nil, ErrWorkspace
	}
	return out, nil
}
