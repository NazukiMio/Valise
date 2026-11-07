// Package sysfix reads the two system settings the health page checks for
// Valise's own needs (docs/SERVICE_CONTRACTS.md, 16.E):
// linger, so the user's timers run without a login, and the server clock's
// time synchronisation. Reading changes nothing and runs on the daily key.
// The fixes themselves are fixed commands the client runs on a fresh admin
// connection; porter only records their audit entry here.
package sysfix

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"valise/core/install"
	"valise/core/vaultrpc"
	porterenroll "valise/porter/internal/enroll"
	"valise/porter/internal/seal"
)

// Wire contract. The client mirrors these types; they belong in core once
// the methods are wired into the tier table.
const (
	MethodStatus = "system_status"
	MethodAudit  = "system_fix_audit"
	Capability   = "system-fix/1"
	AuditOp      = "system_fix"
)

// Tri-state values: a read that failed is never reported as "no".
const (
	Yes     = "yes"
	No      = "no"
	Unknown = "unknown"
)

// Status is what the health page needs. Now is the server's clock.
type Status struct {
	Now             time.Time `json:"now"`
	User            string    `json:"user,omitempty"`
	Linger          string    `json:"linger"`
	CanNTP          string    `json:"can_ntp"`
	NTP             string    `json:"ntp"`
	NTPSynchronized string    `json:"ntp_synchronized"`
	// Timesyncd is the LoadState of systemd-timesyncd.service: "loaded",
	// "not-found", or "unknown".
	Timesyncd string `json:"timesyncd"`
}

// LingerDir is where logind records lingering users (one empty file each).
var LingerDir = "/var/lib/systemd/linger"

const readTimeout = 10 * time.Second

// Read reports the settings; every field is read independently.
func Read(ctx context.Context, e *porterenroll.Env, home string) Status {
	st := Status{Now: time.Now().UTC(), User: e.Get("USER"), Linger: Unknown, CanNTP: Unknown, NTP: Unknown,
		NTPSynchronized: Unknown, Timesyncd: Unknown}
	if !ValidUser(st.User) {
		st.User = ""
	}
	st.Linger = linger(ctx, e, home, st.User)
	if out, ok := run(ctx, e, home, "timedatectl", "show", "-p", "CanNTP", "-p", "NTP", "-p", "NTPSynchronized"); ok {
		v := props(out)
		st.CanNTP, st.NTP, st.NTPSynchronized = yesNo(v["CanNTP"]), yesNo(v["NTP"]), yesNo(v["NTPSynchronized"])
	}
	if out, ok := run(ctx, e, home, "systemctl", "show", "systemd-timesyncd.service", "-p", "LoadState", "--value"); ok {
		switch out = strings.TrimSpace(out); out {
		case "loaded", "not-found":
			st.Timesyncd = out
		}
	}
	return st
}

func linger(ctx context.Context, e *porterenroll.Env, home, user string) string {
	if user == "" {
		return Unknown
	}
	if fi, err := os.Stat(LingerDir); err == nil && fi.IsDir() {
		_, err := os.Stat(filepath.Join(LingerDir, user))
		switch {
		case err == nil:
			return Yes
		case errors.Is(err, os.ErrNotExist):
			return No
		}
	}
	if out, ok := run(ctx, e, home, "loginctl", "show-user", user, "-p", "Linger", "--value"); ok {
		return yesNo(strings.TrimSpace(out))
	}
	return Unknown
}

func run(ctx context.Context, e *porterenroll.Env, home string, argv ...string) (string, bool) {
	out, err := e.Run(ctx, readTimeout, home, argv...)
	if err != nil || out.Code != 0 {
		return "", false
	}
	return string(out.Stdout), true
}

func props(out string) map[string]string {
	v := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if k, val, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			v[k] = val
		}
	}
	return v
}

func yesNo(s string) string {
	switch s {
	case "yes":
		return Yes
	case "no":
		return No
	}
	return Unknown
}

// ValidUser accepts ordinary login names only; anything else is not used
// in a command line.
func ValidUser(u string) bool {
	if u == "" || len(u) > 32 {
		return false
	}
	for i, r := range u {
		switch {
		case r >= 'a' && r <= 'z', r == '_':
		case i > 0 && (r >= '0' && r <= '9' || r == '-' || r == '.'):
		case i > 0 && r == '$' && i == len(u)-1:
		default:
			return false
		}
	}
	return true
}

// Fixes, modes and verification results the audit accepts.
var (
	fixes        = map[string]bool{"linger": true, "clock": true}
	modes        = map[string]bool{"direct": true, "advanced": true}
	auditResults = map[string]bool{"resolved": true, "remaining": true, "unknown": true, "failed": true}
)

// AuditParams record one fix: its commands and exit codes, never output.
type AuditParams struct {
	Fix      string              `json:"fix"`
	Instance string              `json:"instance,omitempty"`
	Mode     string              `json:"mode"`
	Steps    []install.StepAudit `json:"steps"`
	Result   string              `json:"result"`
}

// ErrInvalid is returned for an audit entry outside the fixed vocabulary.
var ErrInvalid = errors.New("invalid system fix audit")

// Audit appends the entry to the server's audit log.
func Audit(home string, p AuditParams) error {
	if !fixes[p.Fix] || !modes[p.Mode] || !auditResults[p.Result] || len(p.Steps) > 4 || len(p.Instance) > 128 {
		return ErrInvalid
	}
	var steps []string
	for _, st := range p.Steps {
		if len(st.Command) > 256 || !utf8.ValidString(st.Command) || strings.ContainsAny(st.Command, "\n\r") {
			return ErrInvalid
		}
		steps = append(steps, st.Command+" exit "+strconv.Itoa(st.Exit))
	}
	reason := p.Fix + " (" + p.Mode + ")"
	if len(steps) > 0 {
		reason += ": " + strings.Join(steps, "; ")
	}
	seal.Paths{Home: home}.AppendAudit(seal.AuditEntry{Time: time.Now().UTC(), Op: AuditOp, Instance: p.Instance,
		Initiator: vaultrpc.InitiatorClient, Result: p.Result, Reason: reason})
	return nil
}
