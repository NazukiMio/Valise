package service

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"valise/client/ssh"
	"valise/client/tunnel"
	"valise/core/valise"
)

// fake-ssh has no -W (AGENT.md); the tunnel dials a local HTTP server.
func TestOpenEntryStepsAndIdle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }))
	defer srv.Close()
	srvPort, _ := strconv.Atoi(srv.URL[strings.LastIndex(srv.URL, ":")+1:])
	old := entryDialer
	entryDialer = func(*ssh.Transport, string, int) tunnel.Dialer {
		return func(ctx context.Context) (io.ReadWriteCloser, error) {
			return net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(srvPort))
		}
	}
	t.Cleanup(func() { entryDialer = old })

	s, _ := loginWorld(t)
	p := filepath.Join(s.Dirs.Data, "profiles", "demo.yaml")
	port := freePort(t)
	os.WriteFile(p, []byte(demoProfile+"entries: [ { id: web, kind: web, host: 127.0.0.1, port: "+strconv.Itoa(port)+", path: \"/ui\" } ]\n"+
		"health: { checks: [ { kind: http, entry: web, expect_status: [200, 401] } ] }\n"), 0o644)

	var mu sync.Mutex
	steps := map[string]EntryStatus{}
	var ready string
	start := time.Now()
	err := s.OpenEntry(context.Background(), "box", "demo", OpenOptions{Idle: 1500 * time.Millisecond,
		Status: func(st EntryStatus) { mu.Lock(); steps[st.Step] = st; mu.Unlock() },
		Ready:  func(u, display string) { ready = u }})
	if err != nil {
		t.Fatal(err)
	}
	if !steps[EntryTunnel].OK {
		t.Fatalf("tunnel: %+v", steps[EntryTunnel])
	}
	if !steps[EntryHealth].OK || steps[EntryHealth].Detail != "HTTP 401" {
		t.Fatalf("health: %+v", steps[EntryHealth])
	}
	if _, ok := steps[EntryRemote]; !ok {
		t.Fatal("remote step not reported")
	}
	if ready != "http://127.0.0.1:"+strconv.Itoa(port)+"/ui" {
		t.Fatalf("ready %q", ready)
	}
	if time.Since(start) < 1500*time.Millisecond {
		t.Fatal("closed before the idle timeout")
	}
}

func freePort(t *testing.T) int {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// A busy local port is an error naming the process, never another port;
// the two-sided change is offered only when the profile declares it.
func TestOpenEntryBusyPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	s, _ := loginWorld(t)
	p := filepath.Join(s.Dirs.Data, "profiles", "demo.yaml")
	for _, tc := range []struct{ extra, want string }{
		{"", ""},
		{", port_change: { steps: [ [demo, port, \"${PORT}\"] ], restart_service: true }", "--change-port"},
	} {
		os.WriteFile(p, []byte(demoProfile+"entries: [ { id: web, kind: web, host: 127.0.0.1, port: "+port+", path: /"+tc.extra+" } ]\n"), 0o644)
		err := s.OpenEntry(context.Background(), "box", "demo", OpenOptions{})
		ue, ok := valise.As(err)
		if !ok || ue.Code != EntryPortBusyCode || !strings.Contains(ue.Reason, port) {
			t.Fatalf("%v", err)
		}
		if got := strings.Contains(ue.Suggestion, "--change-port"); got != (tc.want != "") {
			t.Fatalf("suggestion %q", ue.Suggestion)
		}
	}
}

func TestOpenEntryInitialSuffixHiddenFromDisplay(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer srv.Close()
	srvPort, _ := strconv.Atoi(srv.URL[strings.LastIndex(srv.URL, ":")+1:])
	old := entryDialer
	entryDialer = func(*ssh.Transport, string, int) tunnel.Dialer {
		return func(ctx context.Context) (io.ReadWriteCloser, error) {
			return net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(srvPort))
		}
	}
	t.Cleanup(func() { entryDialer = old })

	s, _ := loginWorld(t)
	p := filepath.Join(s.Dirs.Data, "profiles", "demo.yaml")
	port := freePort(t)
	os.WriteFile(p, []byte(demoProfile+"entries: [ { id: web, kind: web, host: 127.0.0.1, port: "+strconv.Itoa(port)+", path: \"/\" } ]\n"), 0o644)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var gotOpen, gotDisplay string
	var authStatus EntryStatus
	err := s.OpenEntry(ctx, "box", "demo", OpenOptions{
		InitialSuffix: "/?token=secret-one-time-tok",
		Status: func(st EntryStatus) {
			if st.Step == EntryAuth {
				authStatus = st
			}
		},
		Ready: func(open, display string) {
			gotOpen = open
			gotDisplay = display
			cancel()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantOpen := "http://127.0.0.1:" + strconv.Itoa(port) + "/?token=secret-one-time-tok"
	wantDisplay := "http://127.0.0.1:" + strconv.Itoa(port) + "/"
	if gotOpen != wantOpen {
		t.Fatalf("open URL = %q, want %q", gotOpen, wantOpen)
	}
	if gotDisplay != wantDisplay {
		t.Fatalf("display URL = %q, want %q", gotDisplay, wantDisplay)
	}
	if strings.Contains(gotDisplay, "token") || strings.Contains(gotDisplay, "secret-one-time-tok") {
		t.Fatalf("display URL leaked token: %q", gotDisplay)
	}
	if !authStatus.OK {
		t.Fatalf("expected EntryAuth OK when InitialSuffix provided, got %+v", authStatus)
	}
}

func TestOpenEntryUsesAuthArgvUnlessInitialSuffixHasToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer srv.Close()
	srvPort, _ := strconv.Atoi(srv.URL[strings.LastIndex(srv.URL, ":")+1:])
	old := entryDialer
	entryDialer = func(*ssh.Transport, string, int) tunnel.Dialer {
		return func(ctx context.Context) (io.ReadWriteCloser, error) {
			return net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(srvPort))
		}
	}
	t.Cleanup(func() { entryDialer = old })

	s, home := loginWorld(t)
	callsFile := filepath.Join(home, "auth-calls")
	port := freePort(t)
	authScript := fmt.Sprintf("#!/bin/sh\necho call >> %q\necho 'http://127.0.0.1:%d/#ticket=fresh-once-tok'\n", callsFile, port)
	if err := os.WriteFile(filepath.Join(home, ".local/bin/demo-auth"), []byte(authScript), 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(s.Dirs.Data, "profiles", "demo.yaml")
	profYAML := demoProfile +
		"entries: [ { id: web, kind: web, host: 127.0.0.1, port: " + strconv.Itoa(port) + ", path: \"/\", auth: { url_argv: [demo-auth] } } ]\n"
	if err := os.WriteFile(p, []byte(profYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	// 1. Without InitialSuffix (e.g. already listening): OpenEntry runs entryAuth.
	ctx, cancel := context.WithCancel(context.Background())
	var open1, disp1 string
	if err := s.OpenEntry(ctx, "box", "demo", OpenOptions{
		Ready: func(open, display string) { open1, disp1 = open, display; cancel() },
	}); err != nil {
		t.Fatal(err)
	}
	if open1 != "http://127.0.0.1:"+strconv.Itoa(port)+"/#ticket=fresh-once-tok" || disp1 != "http://127.0.0.1:"+strconv.Itoa(port)+"/" {
		t.Fatalf("open1=%q disp1=%q", open1, disp1)
	}

	// 2. With tokenized InitialSuffix (e.g. captured by StartEntry): OpenEntry skips duplicate entryAuth call.
	ctx2, cancel2 := context.WithCancel(context.Background())
	var open2 string
	if err := s.OpenEntry(ctx2, "box", "demo", OpenOptions{
		InitialSuffix: "/#ticket=captured-start-tok",
		Ready:         func(open, _ string) { open2 = open; cancel2() },
	}); err != nil {
		t.Fatal(err)
	}
	if open2 != "http://127.0.0.1:"+strconv.Itoa(port)+"/#ticket=captured-start-tok" {
		t.Fatalf("open2=%q", open2)
	}
	rawCalls, _ := os.ReadFile(callsFile)
	if lines := strings.Split(strings.TrimSpace(string(rawCalls)), "\n"); len(lines) != 1 {
		t.Fatalf("expected 1 entryAuth invocation (skipped duplicate when InitialSuffix had token), got %d (%q)", len(lines), string(rawCalls))
	}
}
