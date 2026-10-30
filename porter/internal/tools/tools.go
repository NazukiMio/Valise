// Package tools detects the server-side tools hello reports.
package tools

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"valise/core/rpc"
)

// userBusTimeout bounds the systemctl --user probe.
const userBusTimeout = 3 * time.Second

// Detect reports every tool in rpc.ToolNames, in order. lookPath searches
// the login PATH, the same one enrollment and revive use; a non-login ssh
// session often lacks user-level installs such as ~/.local/bin.
func Detect(ctx context.Context, lookPath func(string) (string, error)) []rpc.Tool {
	out := make([]rpc.Tool, 0, len(rpc.ToolNames))
	for _, name := range rpc.ToolNames {
		if name == rpc.ToolSystemctlUser {
			out = append(out, systemctlUser(ctx, lookPath))
			continue
		}
		t := rpc.Tool{Name: name}
		if p, err := lookPath(name); err == nil {
			t.Available, t.Path = true, p
		}
		out = append(out, t)
	}
	return out
}

// systemctlUser checks that a user manager is reachable, not only that the
// binary exists: without a user bus, --user units cannot be managed.
func systemctlUser(ctx context.Context, lookPath func(string) (string, error)) rpc.Tool {
	t := rpc.Tool{Name: rpc.ToolSystemctlUser}
	p, err := lookPath("systemctl")
	if err != nil {
		return t
	}
	t.Path = p
	ctx, cancel := context.WithTimeout(ctx, userBusTimeout)
	defer cancel()
	// is-system-running exits non-zero for "degraded", so judge by output.
	b, _ := exec.CommandContext(ctx, p, "--user", "is-system-running").Output()
	state := strings.TrimSpace(string(b))
	t.Detail = state
	switch state {
	case "running", "degraded", "starting", "initializing", "maintenance":
		t.Available = true
	}
	if state == "" {
		t.Detail = "无法连接用户级 systemd"
	}
	return t
}
