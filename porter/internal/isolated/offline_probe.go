package isolated

import (
	"bufio"
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

func awaitOffline(ctx context.Context, check ProbeCheck, wait <-chan struct{}, result ProbeResult) ProbeResult {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			result.FailureCode = "offline_not_ready"
			return result
		case <-wait:
			result.FailureCode = "program_exited"
			return result
		default:
		}
		listening, responded, passed := offlineAttempt(ctx, check)
		result.SocketListening = result.SocketListening || listening
		result.OfflineResponded = result.OfflineResponded || responded
		if passed {
			select {
			case <-wait:
				result.FailureCode = "program_exited"
				return result
			default:
			}
			result.GatewayPassed = true
			return result
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
	}
}

func offlineAttempt(ctx context.Context, check ProbeCheck) (listening, responded, passed bool) {
	r := check.Offline
	// Exact private socket, no pointers or discovery fallback. Symlink parents
	// must not redirect a request to a different private mount or control path.
	real, err := filepath.EvalSymlinks(r.Socket)
	if err != nil || real != r.Socket {
		return
	}
	st, err := os.Lstat(r.Socket)
	if err != nil || st.Mode()&os.ModeSocket == 0 || st.Mode().Perm()&0o077 != 0 {
		return
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Getuid()) {
		return
	}
	conn, err := (&net.Dialer{Timeout: 200 * time.Millisecond}).DialContext(ctx, "unix", r.Socket)
	if err != nil {
		return
	}
	defer conn.Close()
	listening = true
	unixConn, ok := conn.(*net.UnixConn)
	if !ok || !peerOwned(unixConn) {
		return
	}
	deadline := time.Now().Add(500 * time.Millisecond)
	if until, ok := ctx.Deadline(); ok && until.Before(deadline) {
		deadline = until
	}
	if conn.SetDeadline(deadline) != nil {
		return
	}
	request := r.Request + "\n"
	if n, err := io.WriteString(conn, request); err != nil || n != len(request) {
		return
	}
	const maximum = 512 << 10
	line, err := bufio.NewReader(io.LimitReader(conn, maximum+1)).ReadBytes('\n')
	if err != nil || len(line) > maximum {
		return
	}
	object, ok := uniqueJSON(line)
	if !ok {
		return
	}
	responded = true
	passed = offlineMatches(object, r)
	return
}
