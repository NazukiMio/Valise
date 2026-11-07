package stationsync

import (
	"errors"
	"strings"

	"valise/core/valise"
)

// Fixed copy failure causes (docs/SERVICE_CONTRACTS.md 16.G1).
const (
	CauseHostUnknown       = "host_unknown"
	CauseConnectRefused    = "connect_refused"
	CauseConnectTimeout    = "connect_timeout"
	CauseAuthDenied        = "auth_denied"
	CauseHostKeyUnverified = "host_key_unverified"
	CauseNotWritable       = "not_writable"
	CauseToolMissing       = "tool_missing"
	CauseOther             = "other"
)

// Cause classifies the standard error of ssh, rsync or sftp. Only the
// returned code is kept, never the text itself.
func Cause(stderr string) string {
	switch {
	case strings.Contains(stderr, "Could not resolve hostname"), strings.Contains(stderr, "Name or service not known"):
		return CauseHostUnknown
	case strings.Contains(stderr, "Host key verification failed"), strings.Contains(stderr, "REMOTE HOST IDENTIFICATION HAS CHANGED"):
		return CauseHostKeyUnverified
	case strings.Contains(stderr, "Connection refused"):
		return CauseConnectRefused
	case strings.Contains(stderr, "timed out"), strings.Contains(stderr, "No route to host"), strings.Contains(stderr, "Network is unreachable"):
		return CauseConnectTimeout
	case strings.Contains(stderr, "Permission denied (publickey"), strings.Contains(stderr, "Permission denied, please try again"),
		strings.Contains(stderr, "Too many authentication failures"):
		return CauseAuthDenied
	case strings.Contains(stderr, "Permission denied"), strings.Contains(stderr, "Read-only file system"), strings.Contains(stderr, "No space left on device"):
		return CauseNotWritable
	case strings.Contains(stderr, "command not found"), strings.Contains(stderr, "rsync: not found"), strings.Contains(stderr, "subsystem request failed"):
		return CauseToolMissing
	case strings.Contains(stderr, "invalid_grant"), strings.Contains(stderr, "couldn't fetch token"), strings.Contains(stderr, "401 Unauthorized"),
		strings.Contains(stderr, "didn't find section in config file"):
		return CauseAuthDenied
	case strings.Contains(stderr, "no such host"), strings.Contains(stderr, "dial tcp"):
		return CauseConnectTimeout
	}
	return CauseOther
}

// CauseOf classifies a failed copy's error; "" for nil.
func CauseOf(err error) string {
	if err == nil {
		return ""
	}
	var missing *errNoRsync
	if errors.As(err, &missing) {
		return CauseToolMissing
	}
	if ue, ok := valise.As(err); ok {
		if c := Cause(ue.Detail); c != CauseOther {
			return c
		}
		return Cause(ue.Reason)
	}
	return Cause(err.Error())
}
