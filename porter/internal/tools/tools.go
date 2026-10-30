// Package tools detects the server-side tools hello reports.
package tools

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"valise/core/rpc"
)

// LookPath is exec.LookPath; tests replace it.
var LookPath = exec.LookPath

// userBusTimeout bounds the systemctl --user probe.
const userBusTimeout = 3 * time.Second

// Detect reports every tool in rpc.ToolNames, in order.
func Detect(ctx context.Context) []rpc.Tool {
	out := make([]rpc.Tool, 0, len(rpc.ToolNames))
	for _, name := range rpc.ToolNames {
		if name == rpc.ToolSystemctlUser {
			out = append(out, systemctlUser(ctx))
			continue
		}
		t := rpc.Tool{Name: name}
		if p, err := LookPath(name); err == nil {
			t.Available, t.Path = true, p
		}
		out = append(out, t)
	}
	return out
}

// systemctlUser checks that a user manager is reachable, not only that the
// binary exists: without a user bus, --user units cannot be managed.
func systemctlUser(ctx context.Context) rpc.Tool {
	t := rpc.Tool{Name: rpc.ToolSystemctlUser}
	p, err := LookPath("systemctl")
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
