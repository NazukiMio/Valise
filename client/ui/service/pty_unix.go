//go:build darwin || linux

package service

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"valise/core/valise"
)

type localPTY struct {
	mu       sync.Mutex
	masterFD int
	wakeRFD  int
	wakeR    *os.File
	wakeW    *os.File
	closed   bool
	refs     int
}

func openLocalPTY() (*localPTY, *os.File, error) {
	masterFD, slaveName, err := openPTYMaster()
	if err != nil {
		return nil, nil, err
	}
	slave, err := os.OpenFile(slaveName, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		_ = unix.Close(masterFD)
		return nil, nil, err
	}
	wakeR, wakeW, err := os.Pipe()
	if err != nil {
		_ = slave.Close()
		_ = unix.Close(masterFD)
		return nil, nil, err
	}
	p := &localPTY{
		masterFD: masterFD,
		wakeRFD:  int(wakeR.Fd()),
		wakeR:    wakeR,
		wakeW:    wakeW,
		refs:     1,
	}
	_ = p.Resize(80, 24, 0, 0)
	return p, slave, nil
}

func (p *localPTY) acquire() (int, int, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return -1, -1, false
	}
	p.refs++
	return p.masterFD, p.wakeRFD, true
}

func (p *localPTY) release() {
	p.mu.Lock()
	p.refs--
	closeNow := p.refs == 0
	masterFD := p.masterFD
	wakeR := p.wakeR
	p.mu.Unlock()
	if closeNow {
		_ = unix.Close(masterFD)
		if wakeR != nil {
			_ = wakeR.Close()
		}
	}
}

func (p *localPTY) isClosed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed
}

func (p *localPTY) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.refs--
	closeNow := p.refs == 0
	masterFD := p.masterFD
	wakeR := p.wakeR
	wakeW := p.wakeW
	p.wakeW = nil
	p.mu.Unlock()
	if wakeW != nil {
		_ = wakeW.Close()
	}
	if closeNow {
		_ = unix.Close(masterFD)
		if wakeR != nil {
			_ = wakeR.Close()
		}
	}
	return nil
}

func clampWinsize(v, lo, hi int) uint16 {
	if v < lo {
		v = lo
	}
	if v > hi {
		v = hi
	}
	return uint16(v)
}

func (p *localPTY) Resize(cols, rows, wpx, hpx int) error {
	fd, _, ok := p.acquire()
	if !ok {
		return os.ErrClosed
	}
	defer p.release()
	ws := &unix.Winsize{
		Col:    clampWinsize(cols, 1, 0xFFFF),
		Row:    clampWinsize(rows, 1, 0xFFFF),
		Xpixel: clampWinsize(wpx, 0, 0xFFFF),
		Ypixel: clampWinsize(hpx, 0, 0xFFFF),
	}
	return unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, ws)
}

func (p *localPTY) Read(buf []byte) (int, error) {
	if len(buf) == 0 {
		return 0, nil
	}
	fd, wakeRFD, ok := p.acquire()
	if !ok {
		return 0, io.EOF
	}
	defer p.release()
	for {
		if p.isClosed() {
			return 0, io.EOF
		}
		n, err := unix.Read(fd, buf)
		if n > 0 {
			return n, nil
		}
		if err == nil || errors.Is(err, unix.EIO) || errors.Is(err, unix.EBADF) {
			return 0, io.EOF
		}
		if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EINTR) {
			fds := []unix.PollFd{
				{Fd: int32(fd), Events: unix.POLLIN},
				{Fd: int32(wakeRFD), Events: unix.POLLIN},
			}
			_, perr := unix.Poll(fds, -1)
			if errors.Is(perr, unix.EINTR) {
				continue
			}
			if perr != nil || fds[1].Revents != 0 {
				return 0, io.EOF
			}
			// On macOS, slave close can raise POLLHUP without POLLIN while
			// nonblocking read returns EAGAIN; read any buffered bytes then EOF.
			if fds[0].Revents&(unix.POLLHUP|unix.POLLERR|unix.POLLNVAL) != 0 && fds[0].Revents&unix.POLLIN == 0 {
				if n, _ := unix.Read(fd, buf); n > 0 {
					return n, nil
				}
				return 0, io.EOF
			}
			continue
		}
		return 0, err
	}
}

func (p *localPTY) Write(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	fd, wakeRFD, ok := p.acquire()
	if !ok {
		return 0, os.ErrClosed
	}
	defer p.release()
	written := 0
	for written < len(data) {
		if p.isClosed() {
			return written, os.ErrClosed
		}
		n, err := unix.Write(fd, data[written:])
		if n > 0 {
			written += n
			if written == len(data) {
				return written, nil
			}
		}
		if err == nil {
			continue
		}
		if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EINTR) {
			fds := []unix.PollFd{
				{Fd: int32(fd), Events: unix.POLLOUT},
				{Fd: int32(wakeRFD), Events: unix.POLLIN},
			}
			_, perr := unix.Poll(fds, -1)
			if errors.Is(perr, unix.EINTR) {
				continue
			}
			if perr != nil || fds[1].Revents != 0 || fds[0].Revents&(unix.POLLHUP|unix.POLLERR|unix.POLLNVAL) != 0 {
				return written, os.ErrClosed
			}
			continue
		}
		return written, err
	}
	return written, nil
}

// interactiveTermEnv ensures SSH sees xterm-256color, truecolor, and a UTF-8
// locale (important when macOS Finder launches the desktop app without TERM),
// while preserving askpass and agent settings already in base.
func interactiveTermEnv(base []string) []string {
	var env []string
	if len(base) > 0 {
		env = append(env, base...)
	} else {
		env = append(env, os.Environ()...)
	}
	setVar := func(kv string) {
		k, _, _ := strings.Cut(kv, "=")
		for i, e := range env {
			if strings.HasPrefix(e, k+"=") {
				env[i] = kv
				return
			}
		}
		env = append(env, kv)
	}
	setVar("TERM=xterm-256color")
	setVar("COLORTERM=truecolor")
	hasLocale := false
	for _, e := range env {
		k, v, ok := strings.Cut(e, "=")
		if ok && v != "" && (k == "LANG" || k == "LC_ALL" || k == "LC_CTYPE") {
			hasLocale = true
			break
		}
	}
	if !hasLocale {
		setVar("LANG=en_US.UTF-8")
	}
	return env
}

func runSSHWithPTY(ctx context.Context, cmd *exec.Cmd, streams NativeIO, connFailedErr, startFailedErr *valise.UserError) (int, error) {
	if err := ctx.Err(); err != nil {
		return -1, err
	}
	pty, slave, err := openLocalPTY()
	if err != nil {
		return -1, startFailedErr.WithDetail(err.Error())
	}
	if streams.OnResize != nil {
		streams.OnResize(func(cols, rows, wpx, hpx int) {
			_ = pty.Resize(cols, rows, wpx, hpx)
		})
		defer streams.OnResize(nil)
	}
	cmd.Env = interactiveTermEnv(cmd.Env)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	cmd.Cancel = killProcessGroupCancel(cmd)
	cmd.WaitDelay = 500 * time.Millisecond

	if err := cmd.Start(); err != nil {
		_ = slave.Close()
		_ = pty.Close()
		return mapInteractiveExit(ctx, err, connFailedErr, startFailedErr)
	}
	_ = slave.Close()

	waitDone := make(chan struct{})
	if ctx != nil {
		go func() {
			select {
			case <-ctx.Done():
				_ = pty.Close()
			case <-waitDone:
			}
		}()
	}

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		dst := streams.Stdout
		if dst == nil {
			dst = streams.Stderr
		}
		if dst == nil {
			dst = io.Discard
		}
		_, _ = io.Copy(dst, pty)
	}()

	if streams.Stdin != nil {
		go func() {
			_, _ = io.Copy(pty, streams.Stdin)
		}()
	}

	err = cmd.Wait()
	close(waitDone)

	select {
	case <-readDone:
	case <-time.After(150 * time.Millisecond):
		_ = pty.Close()
		<-readDone
	}
	_ = pty.Close()
	return mapInteractiveExit(ctx, err, connFailedErr, startFailedErr)
}
