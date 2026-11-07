package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"
	"valise/core/rpc"
)

func TestEOFInterruptsActiveHandler(t *testing.T) {
	entered := make(chan struct{})
	canceled := make(chan struct{})
	const method = "test-wait-for-disconnect"
	methods[method] = func(s *session, ctx context.Context, raw json.RawMessage) (any, error) {
		close(entered)
		<-ctx.Done()
		close(canceled)
		return struct{}{}, nil
	}
	defer delete(methods, method)
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- Serve(context.Background(), reader, &out, Config{Home: t.TempDir(), DetectTools: func(context.Context) []rpc.Tool { return nil }})
	}()
	if _, err := io.WriteString(writer, helloReq+"\n"+`{"jsonrpc":"2.0","id":2,"method":"`+method+`"}`+"\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not start")
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("disconnect did not cancel active handler")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not exit")
	}
}
