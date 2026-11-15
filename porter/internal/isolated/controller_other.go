//go:build !linux

package isolated

import "net"

func protectController() error { return ErrUnsupported }

func peerOwned(*net.UnixConn) bool { return false }
