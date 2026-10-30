package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"testing"

	"valise/core/rpc"
)

func TestVersionJSON(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"version", "--json"}, nil, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	var v rpc.VersionInfo
	if err := json.Unmarshal(out.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	exe, _ := os.Executable()
	b, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	if v.SelfSHA256 != hex.EncodeToString(sum[:]) || v.OS != runtime.GOOS || v.Protocol != rpc.ProtocolVersion {
		t.Fatalf("%+v", v)
	}
}

func TestUsage(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"serve"}, nil, &out, &errb); code != 2 || !strings.Contains(errb.String(), "用法") {
		t.Fatalf("code %d %q", code, errb.String())
	}
}

func TestRPCOverStdio(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var out, errb bytes.Buffer
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"hello","params":{"protocol":"1.0"}}` + "\n")
	if code := run([]string{"rpc"}, in, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), `"porter_version"`) {
		t.Fatal(out.String())
	}
}
