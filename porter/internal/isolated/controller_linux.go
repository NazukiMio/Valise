package isolated

import (
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// Prevent a same-UID candidate from opening the controller's /proc fd/memory
// handles or attaching to it. This changes only this private helper process.
func protectController() error { return unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0) }

func peerOwned(conn *net.UnixConn) bool {
	raw, err := conn.SyscallConn()
	if err != nil {
		return false
	}
	owned := false
	err = raw.Control(func(fd uintptr) {
		cred, err := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		owned = err == nil && cred != nil && cred.Uid == uint32(os.Getuid())
	})
	return err == nil && owned
}
