package service

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"valise/client/ssh"
	"valise/client/tunnel"
	"valise/core/valise"
)

func TestParseForward(t *testing.T) {
	ft, err := ParseForward("http://127.0.0.1:18789/ui?token=SECRET#frag")
	if err != nil || ft.Port != 18789 || ft.Display != "http://127.0.0.1:18789" || ft.open != "http://127.0.0.1:18789/ui?token=SECRET#frag" || ft.path != "/ui" {
		t.Fatalf("%+v %v", ft, err)
	}
	if ft, err := ParseForward("http://[::1]:9000"); err != nil || ft.Port != 9000 || ft.path != "/" {
		t.Fatalf("%+v %v", ft, err)
	}
	if ft, err := ParseForward("8080"); err != nil || ft.Port != 8080 {
		t.Fatalf("%+v %v", ft, err)
	}
	for _, bad := range []string{
		"http://example.com:80/?token=SECRET", "http://10.0.0.1:80/", "http://localhost/?token=SECRET",
		"ftp://127.0.0.1:21/", "file:///etc/passwd", "0", "70000", "http://127.0.0.1:x/?token=SECRET", "%zz?token=SECRET",
	} {
		_, err := ParseForward(bad)
		if err == nil {
			t.Errorf("accepted %q", bad)
			continue
		}
		if ue, _ := valise.As(err); ue != nil && strings.Contains(ue.Reason+ue.Suggestion+ue.Detail, "SECRET") {
			t.Errorf("error shows the query: %v", err)
		}
	}
}

// fake-ssh has no -W (AGENT.md); the tunnel dials a local HTTP server.
func TestForwardPage(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { gotQuery = r.URL.RawQuery }))
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
	port := strconv.Itoa(freePort(t))
	ft, err := ParseForward("http://localhost:" + port + "/p?token=SECRET")
	if err != nil {
		t.Fatal(err)
	}
	var details []string
	var open, display string
	err = s.ForwardPage(context.Background(), "box", ft, OpenOptions{Idle: time.Second,
		Status: func(st EntryStatus) { details = append(details, st.Step+" "+st.Detail) },
		Ready:  func(o, d string) { open, display = o, d }})
	if err != nil {
		t.Fatal(err)
	}
	if open != "http://localhost:"+port+"/p?token=SECRET" || display != "http://localhost:"+port {
		t.Fatalf("open %q display %q", open, display)
	}
	if strings.Contains(strings.Join(details, "\n"), "SECRET") || gotQuery != "" {
		t.Fatalf("query leaked: %v %q", details, gotQuery)
	}
	if !strings.Contains(strings.Join(details, "\n"), "health HTTP 200") {
		t.Fatalf("%v", details)
	}

	// Busy local port: an error naming the process, no other port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	busy, _ := ParseForward(strconv.Itoa(ln.Addr().(*net.TCPAddr).Port))
	if ue, ok := valise.As(s.ForwardPage(context.Background(), "box", busy, OpenOptions{})); !ok || ue.Code != "forward_port_busy" {
		t.Fatalf("%v", ue)
	}
}
