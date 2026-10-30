package revive

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"valise/core/revive"
)

// procRoot is /proc; tests replace it.
var procRoot = "/proc"

// busyProcesses lists the user's processes whose working directory or open
// files are inside one of roots, except those belonging to exceptUnit's
// cgroup (they are stopped before the swap). Without /proc (not Linux) it
// returns nil.
func busyProcesses(roots []string, exceptUnit string) []revive.Process {
	ents, err := os.ReadDir(procRoot)
	if err != nil {
		return nil
	}
	uid := os.Getuid()
	self := os.Getpid()
	inside := func(p string) (string, bool) {
		for _, r := range roots {
			if p == r || strings.HasPrefix(p, r+"/") {
				return p, true
			}
		}
		return "", false
	}
	var out []revive.Process
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == self {
			continue
		}
		dir := filepath.Join(procRoot, e.Name())
		st, err := os.Stat(dir)
		if err != nil {
			continue
		}
		if sys, ok := st.Sys().(*syscall.Stat_t); !ok || int(sys.Uid) != uid {
			continue
		}
		if exceptUnit != "" {
			if cg, err := os.ReadFile(filepath.Join(dir, "cgroup")); err == nil && strings.Contains(string(cg), "/"+exceptUnit) {
				continue
			}
		}
		hit := ""
		if cwd, err := os.Readlink(filepath.Join(dir, "cwd")); err == nil {
			hit, _ = inside(cwd)
		}
		if hit == "" {
			fds, _ := os.ReadDir(filepath.Join(dir, "fd"))
			for _, fd := range fds {
				if t, err := os.Readlink(filepath.Join(dir, "fd", fd.Name())); err == nil {
					if p, ok := inside(t); ok {
						hit = p
						break
					}
				}
			}
		}
		if hit == "" {
			continue
		}
		cmd, _ := os.ReadFile(filepath.Join(dir, "cmdline"))
		c := strings.Join(strings.Fields(strings.ReplaceAll(string(cmd), "\x00", " ")), " ")
		if len(c) > 120 {
			c = c[:120] + "…"
		}
		out = append(out, revive.Process{PID: pid, Command: c, Path: hit})
	}
	return out
}
