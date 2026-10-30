package server

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"valise/core/revive"
	"valise/core/rpc"
	porterrevive "valise/porter/internal/revive"
)

func serve(t *testing.T, home string, lines ...string) []rpc.Response {
	t.Helper()
	var out bytes.Buffer
	cfg := Config{Version: "test", Home: home, DetectTools: func(context.Context) []rpc.Tool {
		return []rpc.Tool{{Name: rpc.ToolGit, Available: true, Path: "/usr/bin/git"}}
	}}
	if err := Serve(context.Background(), strings.NewReader(strings.Join(lines, "\n")+"\n"), &out, cfg); err != nil {
		t.Fatal(err)
	}
	var resps []rpc.Response
	dec := json.NewDecoder(&out)
	for dec.More() {
		var r rpc.Response
		if err := dec.Decode(&r); err != nil {
			t.Fatal(err)
		}
		resps = append(resps, r)
	}
	return resps
}

const helloReq = `{"jsonrpc":"2.0","id":1,"method":"hello","params":{"client_version":"t","protocol":"1.0"}}`

func TestHello(t *testing.T) {
	home := t.TempDir()
	st := porterrevive.Store{Home: home}
	a, b := revive.NewOpID(time.UnixMilli(1)), revive.NewOpID(time.UnixMilli(2))
	for _, op := range []string{b, a} {
		if err := st.Save(&revive.Journal{Plan: revive.Plan{OpID: op, InstanceID: "demo", Mode: revive.ModeRollback}, State: revive.StateLiveMoved}); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(JournalDir(home), "notes.txt"), []byte("x"), 0o600)
	resps := serve(t, home, helloReq)
	if len(resps) != 1 || resps[0].Error != nil {
		t.Fatalf("%+v", resps)
	}
	var h rpc.HelloResult
	if err := json.Unmarshal(resps[0].Result, &h); err != nil {
		t.Fatal(err)
	}
	if h.Home != home || h.Protocol != rpc.ProtocolVersion || h.PorterVersion != "test" || h.Arch == "" {
		t.Fatalf("%+v", h)
	}
	if len(h.PendingOps) != 2 || h.PendingOps[0].OpID != a || h.PendingOps[1].State != revive.StateLiveMoved {
		t.Fatalf("pending = %+v", h.PendingOps)
	}
}

func TestHelloNoJournalDir(t *testing.T) {
	resps := serve(t, t.TempDir(), helloReq)
	var h rpc.HelloResult
	if err := json.Unmarshal(resps[0].Result, &h); err != nil || h.PendingOps == nil || len(h.PendingOps) != 0 {
		t.Fatalf("%s %v", resps[0].Result, err)
	}
}

func TestHelloRequiredFirst(t *testing.T) {
	resps := serve(t, t.TempDir(),
		`{"jsonrpc":"2.0","id":1,"method":"status"}`,
		helloReq,
		`{"jsonrpc":"2.0","id":3,"method":"status"}`,
	)
	if resps[0].Error == nil || resps[0].Error.Code != rpc.CodeHelloRequired {
		t.Fatalf("%+v", resps[0])
	}
	if resps[1].Error != nil {
		t.Fatalf("%+v", resps[1])
	}
	if resps[2].Error == nil || resps[2].Error.Code != rpc.CodeMethodNotFound || string(resps[2].ID) != "3" {
		t.Fatalf("%+v", resps[2])
	}
}

func TestProtocolMismatch(t *testing.T) {
	resps := serve(t, t.TempDir(), `{"jsonrpc":"2.0","id":"a","method":"hello","params":{"protocol":"2.0"}}`)
	e := resps[0].Error
	if e == nil || e.Code != rpc.CodeUserError || e.Data == nil || e.Data.Code != "protocol_mismatch" {
		t.Fatalf("%+v", resps[0])
	}
}

func TestBadInput(t *testing.T) {
	resps := serve(t, t.TempDir(), `not json`, `{"jsonrpc":"1.0","id":1,"method":"hello"}`)
	if resps[0].Error.Code != rpc.CodeParseError || resps[1].Error.Code != rpc.CodeInvalidRequest {
		t.Fatalf("%+v", resps)
	}
}
