// Package install holds the protocol types of installing a harness
// (docs/INSTALL.md).
package install

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"valise/core/profile"
)

// RPC methods (admin tier).
const (
	MethodCheck = "install_check"
	MethodAudit = "install_audit"
	// Sessions Valise started in a terminal (native UI, install steps,
	// shells) that are still running: listed on the daily key, ended only
	// in developer mode (review F05).
	MethodSessions   = "terminal_sessions"
	MethodSessionEnd = "terminal_session_end"
)

// Session is a terminal program Valise started that has not exited.
type Session struct {
	PID       int       `json:"pid"`
	Argv      []string  `json:"argv"`
	StartedAt time.Time `json:"started_at"`
	// Detached: the connection that started it is gone.
	Detached bool `json:"detached"`
}

// SessionsResult lists the live sessions.
type SessionsResult struct {
	Sessions []Session `json:"sessions"`
}

// SessionEndParams names a session by pid; porter ends it only when its
// own record matches the running process.
type SessionEndParams struct {
	PID int `json:"pid"`
}

// Audit operations.
const (
	AuditInstall    = "install"
	AuditPortChange = "port_change"
	AuditAdvanced   = "advanced"
)

// AuditParams record one install attempt in the server's audit log:
// commands and exit codes only, never terminal output.
type AuditParams struct {
	// Op is the audit operation: AuditInstall (default), AuditPortChange
	// or AuditAdvanced.
	Op             string      `json:"op,omitempty"`
	PlanID         string      `json:"plan_id"`
	ProfileID      string      `json:"profile_id"`
	ProfileVersion int         `json:"profile_version"`
	Steps          []StepAudit `json:"steps"`
	Result         string      `json:"result"`
	Instance       string      `json:"instance,omitempty"`
}

// StepAudit is one step's command and exit code.
type StepAudit struct {
	Kind    string `json:"kind"`
	Command string `json:"command"`
	Exit    int    `json:"exit"`
}

// CheckParams are the requirements to check.
type CheckParams struct {
	Requires []profile.Require `json:"requires"`
}

// Requirement is the state of one requirement on the server.
type Requirement struct {
	Binary  string `json:"binary"`
	Need    string `json:"need,omitempty"`
	Path    string `json:"path,omitempty"`
	Version string `json:"version,omitempty"`
	OK      bool   `json:"ok"`
	// Reason explains a requirement that is not met, in plain words.
	Reason string `json:"reason,omitempty"`
}

// CheckResult is the server's state for a plan.
type CheckResult struct {
	OSID        string        `json:"os_id"`
	OSVersion   string        `json:"os_version,omitempty"`
	Requirement []Requirement `json:"requirements"`
	FreeBytes   uint64        `json:"free_bytes"`
}

var versionRe = regexp.MustCompile(`[0-9]+\.[0-9]+(\.[0-9]+)?`)

// ParseVersion finds the first x.y[.z] in a program's --version output.
func ParseVersion(out string) string { return versionRe.FindString(out) }

// Satisfies reports whether version meets need (">=x.y.z"; "" always does).
func Satisfies(version, need string) bool {
	if need == "" {
		return true
	}
	want := strings.TrimPrefix(need, ">=")
	a, b := nums(version), nums(want)
	if a == nil || b == nil {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return true
}

func nums(v string) []int {
	parts := strings.Split(ParseVersion(v), ".")
	if len(parts) == 2 {
		parts = append(parts, "0")
	}
	if len(parts) != 3 {
		return nil
	}
	out := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil
		}
		out[i] = n
	}
	return out
}
