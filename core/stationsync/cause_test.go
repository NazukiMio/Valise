package stationsync

import (
	"errors"
	"testing"

	"valise/core/valise"
)

func TestCause(t *testing.T) {
	cases := map[string]string{
		"ssh: Could not resolve hostname nas-test: Name or service not known": CauseHostUnknown,
		"ssh: connect to host 192.168.101.109 port 22: Connection refused":    CauseConnectRefused,
		"ssh: connect to host 192.168.101.109 port 22: Operation timed out":   CauseConnectTimeout,
		"ssh: connect to host 10.0.0.2 port 22: No route to host":             CauseConnectTimeout,
		"nas@nas: Permission denied (publickey,password).":                    CauseAuthDenied,
		"Host key verification failed.":                                       CauseHostKeyUnverified,
		`rsync: [receiver] mkstemp "/v/x" failed: Permission denied (13)`:     CauseNotWritable,
		"bash: line 1: rsync: command not found":                              CauseToolMissing,
		"something else":                                                      CauseOther,
	}
	for in, want := range cases {
		if got := Cause(in); got != want {
			t.Errorf("%q: got %s want %s", in, got, want)
		}
	}
	if CauseOf(nil) != "" {
		t.Fatal("nil")
	}
	e := valise.New("sync_failed", "复制失败", "").WithDetail("ssh: connect to host h port 22: Connection refused")
	if CauseOf(e) != CauseConnectRefused || CauseOf(&errNoRsync{err: errors.New("x")}) != CauseToolMissing {
		t.Fatal("CauseOf")
	}
}
