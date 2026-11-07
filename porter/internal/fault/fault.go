//go:build faultinject

// Package fault crashes the porter at named points, for crash recovery tests
// on real machines. It exists only in builds with the faultinject tag; release
// builds compile the no-op version in off.go.
package fault

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Enabled reports whether this build can inject faults.
const Enabled = true

// Hit kills the process without cleanup when the one-shot trigger file
// ~/.local/state/valise/fault-inject names point. The file is removed first,
// so a retry after the crash proceeds normally.
func Hit(home, point string) {
	p := filepath.Join(home, ".local", "state", "valise", "fault-inject")
	b, err := os.ReadFile(p)
	if err != nil || strings.TrimSpace(string(b)) != point {
		return
	}
	if os.Remove(p) != nil {
		return
	}
	_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
	select {} // SIGKILL cannot be caught; never return
}
