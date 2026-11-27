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
	// MethodLoopbackListeners lists the loopback TCP ports the porter's own
	// user listens on (capability LoopbackListenersCapability).
	MethodLoopbackListeners = "loopback_listeners"
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
	// Grant lets the daily key run login-run for this session once
	// (docs/ARCHITECTURE.md 2.2); empty on a full login.
	Grant string `json:"grant,omitempty"`
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

// LoopbackListenersCapability is advertised in hello when porter answers
// MethodLoopbackListeners.
const LoopbackListenersCapability = "loopback-listeners/1"

// LoopbackListenersResult lists listening loopback ports, ascending.
type LoopbackListenersResult struct {
	Ports []int `json:"ports"`
}

// EntryLifecycleCapability is advertised in hello when porter supports
// on-demand entry probe, prepare, detached background start, and stop
// (docs/LOGIN_RELAY.md 4.3).
const (
	EntryLifecycleCapability = "entry-lifecycle/1"
	MethodEntryProbe         = "entry_probe"
	MethodEntryPrepare       = "entry_prepare"
	MethodEntryStart         = "entry_start"
	MethodEntryStop          = "entry_stop"
)

// EntryProbeParams asks porter to probe an entry's listening, preparation and
// Valise-managed runtime state.
type EntryProbeParams struct {
	InstanceID string        `json:"instance_id"`
	Entry      profile.Entry `json:"entry"`
}

// EntryProbeResult reports an entry's live listening state, whether prepare is
// satisfied, and whether Valise started a managed process/unit for it.
type EntryProbeResult struct {
	EntryID      string   `json:"entry_id"`
	Port         int      `json:"port"`
	Listening    bool     `json:"listening"`
	ListenAddrs  []string `json:"listen_addrs,omitempty"`
	HasPrepare   bool     `json:"has_prepare"`
	Prepared     bool     `json:"prepared"`
	PrepareNote  string   `json:"prepare_note,omitempty"`
	HasStart     bool     `json:"has_start"`
	AgentHint    string   `json:"agent_hint,omitempty"`
	LocalSource  string   `json:"local_source,omitempty"`
	ManagedByUs  bool     `json:"managed_by_us"`
	ManagedUnit  string   `json:"managed_unit,omitempty"`
	ManagedPID   int      `json:"managed_pid,omitempty"`
	ManagedAlive bool     `json:"managed_alive"`
}

// EntryPrepareParams asks porter to execute an entry's prepare steps.
type EntryPrepareParams struct {
	InstanceID string        `json:"instance_id"`
	Entry      profile.Entry `json:"entry"`
}

// EntryPrepareResult reports the outcome of running an entry's prepare steps.
type EntryPrepareResult struct {
	EntryID          string `json:"entry_id"`
	StepsRun         int    `json:"steps_run"`
	ServiceRestarted bool   `json:"service_restarted,omitempty"`
	Prepared         bool   `json:"prepared"`
	Listening        bool   `json:"listening"`
	Detail           string `json:"detail,omitempty"`
}

// EntryStartParams asks porter to start an entry detached in the background.
type EntryStartParams struct {
	InstanceID string        `json:"instance_id"`
	Entry      profile.Entry `json:"entry"`
}

// EntryStartResult reports the result of starting an entry in the background.
type EntryStartResult struct {
	EntryID     string   `json:"entry_id"`
	Port        int      `json:"port"`
	Listening   bool     `json:"listening"`
	ListenAddrs []string `json:"listen_addrs,omitempty"`
	ManagedByUs bool     `json:"managed_by_us"`
	ManagedUnit string   `json:"managed_unit,omitempty"`
	ManagedPID  int      `json:"managed_pid,omitempty"`
	LogPath     string   `json:"log_path,omitempty"`
	AlreadyUp   bool     `json:"already_up,omitempty"`
	// AuthSuffix is an optional URL suffix (path, query, fragment) captured
	// from startup output when capture_url is enabled. It may carry a token and
	// must never be logged, audited, or displayed in plain text.
	AuthSuffix string `json:"auth_suffix,omitempty"`
}

// EntryStopParams asks porter to stop a Valise-started entry process or unit.
type EntryStopParams struct {
	InstanceID string        `json:"instance_id"`
	Entry      profile.Entry `json:"entry"`
}

// EntryStopResult reports the port and stop status after stopping an entry.
type EntryStopResult struct {
	EntryID     string   `json:"entry_id"`
	Port        int      `json:"port"`
	Stopped     bool     `json:"stopped"`
	Listening   bool     `json:"listening"`
	ListenAddrs []string `json:"listen_addrs,omitempty"`
	Detail      string   `json:"detail,omitempty"`
}
