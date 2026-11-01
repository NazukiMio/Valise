package tunnel

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"testing"
	"time"
)

// Tunnels through real ssh need the target machines (AGENT.md); these tests
// cover the local listeners with an in-process dialer.

func echoDialer(ctx context.Context) (io.ReadWriteCloser, error) {
	a, b := net.Pipe()
	go func() { io.Copy(b, b); b.Close() }()
	return a, nil
}

func freePort(t *testing.T) int {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestForwardAndStats(t *testing.T) {
	port := freePort(t)
	l, err := Bind(port)
	if err != nil {
		t.Fatal(err)
	}
	arrived := make(chan struct{}, 4)
	l.OnConn = func() { arrived <- struct{}{} }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go l.Serve(ctx, echoDialer)

	for _, addr := range l.Addrs {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		c.Write([]byte("ping\n"))
		line, err := bufio.NewReader(c).ReadString('\n')
		if err != nil || line != "ping\n" {
			t.Fatalf("%s: %q %v", addr, line, err)
		}
		<-arrived
		if active, _, idle := l.Stats(); active != 1 || idle != 0 {
			t.Fatalf("active=%d idle=%v", active, idle)
		}
		c.Close()
	}
	time.Sleep(50 * time.Millisecond)
	if active, total, _ := l.Stats(); active != 0 || total != len(l.Addrs) {
		t.Fatalf("active=%d total=%d", active, total)
	}
}

func TestBusyPortIsAnErrorNeverAnotherPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	_, err = Bind(port)
	var busy *BusyError
	if !errors.As(err, &busy) || busy.Port != port {
		t.Fatalf("%v", err)
	}
	ln.Close()
	l, err := Bind(port)
	if err != nil {
		t.Fatal(err)
	}
	l.Close()
	if _, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(l.Port)); err == nil {
		t.Fatal("closed listener still accepts")
	}
}
