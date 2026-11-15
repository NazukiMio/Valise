package isolated

import (
	"path"
	"strings"
	"time"

	"valise/core/profile"
)

// CheckForRuntime binds public readiness metadata to authenticated source
// facts, not the current PATH or a parent installation's present-day version.
func CheckForRuntime(p profile.Profile, home, state, revision string) (ProbeCheck, error) {
	if p.Environments == nil || p.Environments.OfflineReadiness == nil {
		return CheckFor(p)
	}
	r := p.Environments.OfflineReadiness
	if p.Environments.GatewayEntry != "" || !r.Valid() || !privatePathAt(home, state) {
		return ProbeCheck{}, ErrWorkspace
	}
	bind := func(s string) (string, bool) {
		if strings.Contains(s, "${PROGRAM_REVISION}") && (revision == "" || strings.ContainsAny(revision, "\x00\r\n") || strings.Contains(revision, "${")) {
			return "", false
		}
		return strings.NewReplacer("${HOME}", home, "${STATE_DIR}", state, "${PROGRAM_REVISION}", revision).Replace(s), true
	}
	bound := *r
	bound.Socket, _ = bind(r.Socket)
	fields := func(in []profile.ReadinessField) ([]profile.ReadinessField, bool) {
		out := append([]profile.ReadinessField{}, in...)
		for i := range out {
			var ok bool
			out[i].Value, ok = bind(out[i].Value)
			if !ok {
				return nil, false
			}
		}
		return out, true
	}
	var ok bool
	bound.Checks, ok = fields(r.Checks)
	if !ok {
		return ProbeCheck{}, ErrWorkspace
	}
	bound.Members = append([]profile.ReadinessMember{}, r.Members...)
	for i := range bound.Members {
		bound.Members[i].MatchValue, _ = bind(bound.Members[i].MatchValue)
		bound.Members[i].Checks, ok = fields(bound.Members[i].Checks)
		if !ok {
			return ProbeCheck{}, ErrWorkspace
		}
	}
	check := ProbeCheck{Offline: &bound, Timeout: 2 * time.Minute}
	if !check.validAt(home) {
		return ProbeCheck{}, ErrWorkspace
	}
	return check, nil
}

func (p ProbeCheck) validOffline() bool {
	r := p.Offline
	if r == nil || p.Host != "" || p.Port != 0 || p.Path != "" || len(p.Statuses) != 0 || p.Timeout <= 0 || p.Timeout > 2*time.Minute ||
		!path.IsAbs(r.Socket) || path.Clean(r.Socket) != r.Socket || len(r.Socket) > 107 || strings.Contains(r.Socket, "${") {
		return false
	}
	// Reuse the same assertion/request validator after expansion. The socket
	// itself is checked against the descriptor's HOME separately.
	declaration := *r
	declaration.Socket = "${HOME}/probe.sock"
	if !declaration.Valid() {
		return false
	}
	for _, f := range r.Checks {
		if strings.Contains(f.Value, "${") {
			return false
		}
	}
	for _, m := range r.Members {
		if strings.Contains(m.MatchValue, "${") {
			return false
		}
		for _, f := range m.Checks {
			if strings.Contains(f.Value, "${") {
				return false
			}
		}
	}
	return true
}

func (p ProbeCheck) validAt(home string) bool {
	return p.valid() && (p.Offline == nil || privatePathAt(home, p.Offline.Socket) && p.Offline.Socket != home)
}
