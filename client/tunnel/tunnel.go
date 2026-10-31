// Package tunnel forwards local loopback ports to a server through the
// system OpenSSH: the client listens on 127.0.0.1 (and [::1]) itself and
// runs one `ssh -W` per connection (docs/LOGIN_RELAY.md section 5.1).
package tunnel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"valise/client/ssh"
)

// Dialer opens one forwarded connection.
type Dialer func(ctx context.Context) (io.ReadWriteCloser, error)

// BusyError reports a local port already in use.
type BusyError struct {
	Port    int
	Address string
	Process string // "name (pid N)" when it could be found
}

func (e *BusyError) Error() string {
	if e.Process != "" {
		return fmt.Sprintf("本机端口 %d 已被 %s 占用", e.Port, e.Process)
	}
	return fmt.Sprintf("本机端口 %d（%s）已被占用", e.Port, e.Address)
}

// Listener is a set of loopback listeners on one port.
type Listener struct {
	Port  int
	Addrs []string

	lns []net.Listener

	mu       sync.Mutex
	active   int
	total    int
	lastSeen time.Time
	// OnConn is called when a connection arrives (before it is forwarded).
	OnConn func()
}

// Bind listens on 127.0.0.1:port and, when the host has IPv6 loopback,
// [::1]:port. Either address being taken is a BusyError: a browser may
// resolve localhost to either family.
func Bind(port int) (*Listener, error) {
	l := &Listener{Port: port, lastSeen: time.Now()}
	for _, host := range []string{"127.0.0.1", "::1"} {
		addr := net.JoinHostPort(host, strconv.Itoa(port))
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			if errors.Is(err, syscall.EADDRINUSE) || errors.Is(err, syscall.EACCES) || host == "127.0.0.1" {
				l.Close()
				return nil, &BusyError{Port: port, Address: addr, Process: FindProcess(port)}
			}
			continue // no IPv6 loopback on this host
		}
		l.lns = append(l.lns, ln)
		l.Addrs = append(l.Addrs, addr)
	}
	return l, nil
}

// BindFrom tries port, port+1, ... (web entries may move; login relays
// never do).
func BindFrom(port, tries int) (*Listener, error) {
	var last error
	for p := port; p < port+tries && p <= 65535; p++ {
		l, err := Bind(p)
		if err == nil {
			return l, nil
		}
		last = err
	}
	return nil, last
}

// Close stops listening; connections in flight finish on their own.
func (l *Listener) Close() {
	for _, ln := range l.lns {
		ln.Close()
	}
}

// Stats reports active and total connections and the time since the last
// connection ended (0 while any is active).
func (l *Listener) Stats() (active, total int, idle time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.active > 0 {
		return l.active, l.total, 0
	}
	return 0, l.total, time.Since(l.lastSeen)
}

// Serve accepts connections until ctx ends or the listeners close, and
// forwards each one through dial.
func (l *Listener) Serve(ctx context.Context, dial Dialer) {
	var wg sync.WaitGroup
	for _, ln := range l.lns {
		wg.Add(1)
		go func(ln net.Listener) {
			defer wg.Done()
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				go l.forward(ctx, c, dial)
			}
		}(ln)
	}
	go func() { <-ctx.Done(); l.Close() }()
	wg.Wait()
}

func (l *Listener) forward(ctx context.Context, c net.Conn, dial Dialer) {
	l.mu.Lock()
	l.active++
	l.total++
	cb := l.OnConn
	l.mu.Unlock()
	defer func() {
		l.mu.Lock()
		l.active--
		l.lastSeen = time.Now()
		l.mu.Unlock()
	}()
	if cb != nil {
		cb()
	}
	defer c.Close()
	r, err := dial(ctx)
	if err != nil {
		return
	}
	defer r.Close()
	done := make(chan struct{}, 2)
	go func() { io.Copy(r, c); done <- struct{}{} }()
	go func() { io.Copy(c, r); done <- struct{}{} }()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// SSHDialer forwards to host:port as seen from the server, one `ssh -W`
// process per connection over the transport's ControlMaster.
func SSHDialer(t *ssh.Transport, host string, port int) Dialer {
	return func(ctx context.Context) (io.ReadWriteCloser, error) {
		cmd := exec.CommandContext(ctx, t.Binary(), t.ForwardArgs(host, port)...)
		in, err := cmd.StdinPipe()
		if err != nil {
			return nil, err
		}
		out, err := cmd.StdoutPipe()
		if err != nil {
			return nil, err
		}
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		return &procConn{cmd: cmd, in: in, out: out}, nil
	}
}

type procConn struct {
	cmd  *exec.Cmd
	in   io.WriteCloser
	out  io.ReadCloser
	once sync.Once
}

func (p *procConn) Read(b []byte) (int, error)  { return p.out.Read(b) }
func (p *procConn) Write(b []byte) (int, error) { return p.in.Write(b) }
func (p *procConn) Close() error {
	p.once.Do(func() {
		p.in.Close()
		if p.cmd.Process != nil {
			p.cmd.Process.Kill()
		}
		p.cmd.Wait()
	})
	return nil
}

// FindProcess names the process listening on a local TCP port, or "".
func FindProcess(port int) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "lsof", "-nP", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN", "-Fpc").Output()
	if err != nil {
		return ""
	}
	var pid, name string
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "p") && pid == "":
			pid = line[1:]
		case strings.HasPrefix(line, "c") && name == "":
			name = line[1:]
		}
	}
	if pid == "" {
		return ""
	}
	return fmt.Sprintf("%s（pid %s）", name, pid)
}
