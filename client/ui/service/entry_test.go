package service

import (
	"context"
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
	// Entry port taken locally by the test server itself: the tunnel moves up.
	os.WriteFile(p, []byte(demoProfile+"entries: [ { id: web, kind: web, host: 127.0.0.1, port: "+strconv.Itoa(srvPort)+", path: \"/ui\" } ]\n"+
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
	if !steps[EntryTunnel].OK || !strings.Contains(steps[EntryTunnel].Detail, "被占用") {
		t.Fatalf("tunnel: %+v", steps[EntryTunnel])
	}
	if !steps[EntryHealth].OK || steps[EntryHealth].Detail != "HTTP 401" {
		t.Fatalf("health: %+v", steps[EntryHealth])
	}
	if _, ok := steps[EntryRemote]; !ok {
		t.Fatal("remote step not reported")
	}
	if !strings.HasPrefix(ready, "http://127.0.0.1:") || !strings.HasSuffix(ready, "/ui") || strings.Contains(ready, ":"+strconv.Itoa(srvPort)+"/") {
		t.Fatalf("ready %q", ready)
	}
	if time.Since(start) < 1500*time.Millisecond {
		t.Fatal("closed before the idle timeout")
	}
}
