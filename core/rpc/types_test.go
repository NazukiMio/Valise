package rpc

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestCompatible(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"1.0", "1.0", true},
		{"1.0", "1.7", true},
		{"1.0", "2.0", false},
		{"1", "1.0", false},
		{"x.0", "x.0", false},
	}
	for _, c := range cases {
		if got := Compatible(c.a, c.b); got != c.want {
			t.Errorf("Compatible(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

func TestConnRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	w := NewConn(nil, &buf)
	if err := w.Write(Request{JSONRPC: JSONRPCVersion, ID: []byte("1"), Method: MethodHello}); err != nil {
		t.Fatal(err)
	}
	r := NewConn(strings.NewReader("\n"+buf.String()), io.Discard)
	line, err := r.ReadLine()
	if err != nil || !strings.Contains(string(line), `"method":"hello"`) {
		t.Fatalf("line=%q err=%v", line, err)
	}
	if _, err := r.ReadLine(); !errors.Is(err, io.EOF) {
		t.Fatalf("want EOF, got %v", err)
	}
}

func TestConnTooLarge(t *testing.T) {
	r := NewConn(strings.NewReader(strings.Repeat("x", MaxMessageBytes+1)+"\n"), io.Discard)
	if _, err := r.ReadLine(); !errors.Is(err, ErrMessageTooLarge) {
		t.Fatalf("err = %v", err)
	}
}
