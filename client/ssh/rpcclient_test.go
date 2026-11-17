package ssh

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"valise/core/rpc"
	"valise/core/valise"
	"valise/testbed/fakesshenv"
)

func TestRPCRoundTrip(t *testing.T) {
	e := fakesshenv.New(t)
	home := e.AddHost("box")
	porter := e.InstallPorter("box")
	op := "op_01JBX7Q2W8M3ZK5N6P9R4T0Y2A"
	os.MkdirAll(filepath.Join(home, ".local/state/valise/journal"), 0o700)
	os.WriteFile(filepath.Join(home, ".local/state/valise/journal/"+op+".json"),
		[]byte(`{"plan":{"op_id":"`+op+`","instance_id":"demo","mode":"rollback"},"state":"LIVE_MOVED"}`), 0o600)

	ctx := context.Background()
	c, hello, err := Dial(ctx, fakeTransport(t, e, "box"), porter, "test-client")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if hello.Home != home || hello.Protocol != rpc.ProtocolVersion || len(hello.Tools) != len(rpc.ToolNames) {
		t.Fatalf("%+v", hello)
	}
	if len(hello.PendingOps) != 1 || hello.PendingOps[0].OpID != op || hello.PendingOps[0].State != "LIVE_MOVED" {
		t.Fatalf("pending: %+v", hello.PendingOps)
	}
	// A second hello on the same session also round-trips, and unknown
	// methods come back as JSON-RPC errors without killing the session.
	var again rpc.HelloResult
	if err := c.Call(ctx, rpc.MethodHello, rpc.HelloParams{Protocol: rpc.ProtocolVersion}, &again); err != nil || again.Home != home {
		t.Fatalf("%v %+v", err, again)
	}
	err = c.Call(ctx, "nope", nil, nil)
	if re, ok := err.(*rpc.Error); !ok || re.Code != rpc.CodeMethodNotFound {
		t.Fatalf("%v", err)
	}
	if err := c.Call(ctx, rpc.MethodHello, rpc.HelloParams{Protocol: rpc.ProtocolVersion}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestRPCStreamDropped(t *testing.T) {
	e := fakesshenv.New(t)
	e.AddHost("box")
	porter := e.InstallPorter("box")
	t.Setenv("FAKESSH_DROP_AFTER_BYTES", "20")
	_, _, err := Dial(context.Background(), fakeTransport(t, e, "box"), porter, "t")
	ue, ok := valise.As(err)
	if !ok || ue.Code != "ssh_connect_failed" || !strings.Contains(ue.Detail, "closed by remote host") {
		t.Fatalf("%v", err)
	}
}

func TestRPCConnectRefused(t *testing.T) {
	e := fakesshenv.New(t)
	e.AddHost("box")
	t.Setenv("FAKESSH_FAIL_CONNECT", "box")
	_, _, err := Dial(context.Background(), fakeTransport(t, e, "box"), "/x/valise-porter", "t")
	if ue, ok := valise.As(err); !ok || ue.Code != "ssh_connect_failed" {
		t.Fatalf("%v", err)
	}
}

func TestRPCNotAPorter(t *testing.T) {
	e := fakesshenv.New(t)
	e.AddHost("box")
	_, _, err := Dial(context.Background(), fakeTransport(t, e, "box"), "/bin/echo", "t")
	if ue, ok := valise.As(err); !ok || ue.Code != "porter_session_lost" {
		t.Fatalf("%v", err)
	}
}

func TestRPCDelayAndContext(t *testing.T) {
	e := fakesshenv.New(t)
	e.AddHost("box")
	porter := e.InstallPorter("box")
	t.Setenv("FAKESSH_DELAY_MS", "2000")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, err := Dial(ctx, fakeTransport(t, e, "box"), porter, "t")
	if err == nil || time.Since(start) > 5*time.Second {
		t.Fatalf("err=%v after %v", err, time.Since(start))
	}
}

func TestRPCProcessHookStartAndEnd(t *testing.T) {
	e := fakesshenv.New(t)
	e.AddHost("box")
	porter := e.InstallPorter("box")
	var mu sync.Mutex
	var events []ProcessEvent
	tr, err := New("box", Options{
		Binary:     e.SSH,
		ControlDir: t.TempDir(),
		Mode:       ModeAdmin,
		ProcessHook: func(ev ProcessEvent) {
			mu.Lock()
			events = append(events, ev)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	c, _, err := Dial(context.Background(), tr, porter, "t")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	got := append([]ProcessEvent{}, events...)
	mu.Unlock()
	if len(got) != 2 || got[0].Phase != "start" || got[0].Kind != "rpc" || got[0].Mode != ModeAdmin {
		t.Fatalf("unexpected start event: %+v", got)
	}
	if got[1].Phase != "end" || got[1].Kind != "rpc" || got[1].Mode != ModeAdmin || got[1].Host != "box" || got[1].ExitCode != 0 {
		t.Fatalf("unexpected end event: %+v", got)
	}
}
