// Package login holds the RPC types of the login relay
// (docs/LOGIN_RELAY.md section 5).
package login

import (
	"regexp"

	"valise/core/profile"
)

// RPC methods.
const (
	MethodPrepare = "login_prepare"
	MethodURLs    = "login_urls"
	MethodEnd     = "login_end"
	MethodListen  = "listen_addrs"
)

// SessionEnv names the login session for porter browser-shim.
const SessionEnv = "VALISE_LOGIN_SESSION"

// Login kinds.
const (
	KindLoopbackOAuth = "loopback_oauth"
	KindDeviceCode    = "device_code"
	KindPasteCode     = "paste_code"
	KindRemoteBrowser = "remote_browser"
)

// DefaultTimeoutSeconds applies when a login entry sets none.
const DefaultTimeoutSeconds = 300

var sessionRe = regexp.MustCompile(`^ls_[0-9a-f]{32}$`)

// ValidSession reports whether id is a login session id.
func ValidSession(id string) bool { return sessionRe.MatchString(id) }

// PrepareParams asks porter to set up a login session for an instance.
type PrepareParams struct {
	InstanceID string        `json:"instance_id"`
	Login      profile.Login `json:"login"`
}

// PrepareResult names the session and the command it will run.
type PrepareResult struct {
	Session string   `json:"session"`
	Argv    []string `json:"argv"`
}

// SessionParams names a session.
type SessionParams struct {
	Session string `json:"session"`
}

// URLsResult lists the links browser-shim captured, in order.
type URLsResult struct {
	URLs []string `json:"urls"`
}

// EndParams closes a session; the exit code goes to the audit log.
type EndParams struct {
	Session  string `json:"session"`
	ExitCode int    `json:"exit_code"`
}

// ListenParams asks which local addresses listen on a TCP port.
type ListenParams struct {
	Port int `json:"port"`
}

// ListenResult lists listening addresses ("127.0.0.1", "::1", "0.0.0.0",
// "::", ...).
type ListenResult struct {
	Addrs []string `json:"addrs"`
}

// MethodEntryAuth runs an entry's declared token command (LOGIN_RELAY.md 4.1).
const MethodEntryAuth = "entry_auth"

// EntryAuthParams carries the entry from the client's effective profile.
type EntryAuthParams struct {
	InstanceID string        `json:"instance_id"`
	Entry      profile.Entry `json:"entry"`
}

// EntryAuthResult is what to append to the local base URL (scheme, host and
// port are the tunnel's): path, query and fragment, holding the token.
type EntryAuthResult struct {
	Suffix string `json:"suffix"`
}
