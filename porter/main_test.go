package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"valise/core/enroll"
	"valise/core/profile"
	"valise/core/rpc"
	porterenroll "valise/porter/internal/enroll"
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

func TestServeDaily(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	hello := `{"jsonrpc":"2.0","id":1,"method":"hello","params":{"protocol":"1.1"}}` + "\n"
	call := func(cmdline, input string) (int, string, string) {
		t.Setenv("SSH_ORIGINAL_COMMAND", cmdline)
		var out, errb bytes.Buffer
		code := run([]string{"serve", "--tier", "daily"}, strings.NewReader(input), &out, &errb)
		return code, out.String(), errb.String()
	}
	code, out, _ := call(`'$HOME/.local/bin/valise-porter' rpc`, hello+
		`{"jsonrpc":"2.0","id":2,"method":"vault_status","params":{}}`+"\n"+
		`{"jsonrpc":"2.0","id":3,"method":"timer_set","params":{}}`+"\n"+
		`{"jsonrpc":"2.0","id":4,"method":"made_up","params":{}}`+"\n")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 4 || strings.Contains(lines[1], rpc.AdminRequiredCode) || !strings.Contains(lines[2], rpc.AdminRequiredCode) || !strings.Contains(lines[3], "method not found") {
		t.Fatalf("%q", lines)
	}
	if code, _, errs := call("sh -c id", ""); code != 126 || !strings.Contains(errs, "日常操作") {
		t.Fatalf("shell: %d %q", code, errs)
	}
	if code, _, _ := call("", ""); code != 126 {
		t.Fatalf("interactive login: %d", code)
	}
	if code, out, _ := call(`"$HOME/.local/bin/valise-porter" version --json`, ""); code != 0 || !strings.Contains(out, "valise-porter") {
		t.Fatalf("version: %d %q", code, out)
	}
	if code, _, _ := run([]string{"serve", "--tier", "admin"}, nil, &bytes.Buffer{}, &bytes.Buffer{}), "", ""; code != 2 {
		t.Fatalf("only the daily tier is a forced command: %d", code)
	}
}

func TestDailyFirstRecipientOnly(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SSH_ORIGINAL_COMMAND", "valise-porter rpc")
	add := func(id int, r string) string {
		return `{"jsonrpc":"2.0","id":` + string(rune('0'+id)) + `,"method":"recipient_add","params":{"recipient":"` + r + `"}}` + "\n"
	}
	in := `{"jsonrpc":"2.0","id":1,"method":"hello","params":{"protocol":"1.1"}}` + "\n" +
		add(2, "age1ql3z7hjy54pw3hyww5ayyfg7zqgvc7w3j2elw8zmrj2kg5sfn9aqmcac8p") +
		add(3, "age1zvkyg2lqzraa2lnjvqej32nkuu0ues2s82hzrye869xeexvn73equnujwj")
	var out, errb bytes.Buffer
	if code := run([]string{"serve", "--tier", "daily"}, strings.NewReader(in), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 || !strings.Contains(lines[1], `"added":true`) || !strings.Contains(lines[2], rpc.AdminRequiredCode) {
		t.Fatalf("%q", lines)
	}
}

// Read actions run on the daily tier; secret and admin actions are refused
// there and run on a full login (docs/INSTALL.md section 5).
func TestActionTiers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	p := profile.Profile{ID: "demo", ProfileVersion: 1, Actions: []profile.Action{
		{ID: "hello", Name: "h", Explain: "e", Risk: profile.RiskRead, Argv: profile.Argv{"/bin/echo", "hi"}, Output: profile.OutputText},
		{ID: "token", Name: "t", Explain: "e", Risk: profile.RiskRead, Argv: profile.Argv{"/bin/echo", "tok-1"}, Output: profile.OutputSecret, Secret: &profile.SecretPick{}},
		{ID: "restart", Name: "r", Explain: "e", Risk: profile.RiskManage, Argv: profile.Argv{"/bin/echo", "x"}, Output: profile.OutputNone},
	}}
	if _, err := porterenroll.WriteRecord(home, enroll.Record{InstanceID: "demo", ProfileID: "demo", ProfileVersion: 1}, p); err != nil {
		t.Fatal(err)
	}
	call := func(id int, action string) string {
		return `{"jsonrpc":"2.0","id":` + strconv.Itoa(id) + `,"method":"action_run","params":{"instance_id":"demo","action_id":"` + action + `"}}` + "\n"
	}
	in := `{"jsonrpc":"2.0","id":1,"method":"hello","params":{"protocol":"1.2"}}` + "\n" + call(2, "hello") + call(3, "token") + call(4, "restart") + call(5, "nope")
	var out, errb bytes.Buffer
	t.Setenv("SSH_ORIGINAL_COMMAND", "valise-porter rpc")
	if code := run([]string{"serve", "--tier", "daily"}, strings.NewReader(in), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 5 || !strings.Contains(lines[1], `"output":"hi"`) || !strings.Contains(lines[2], rpc.AdminRequiredCode) ||
		!strings.Contains(lines[3], rpc.AdminRequiredCode) || !strings.Contains(lines[4], "action_unknown") || strings.Contains(out.String(), "tok-1") {
		t.Fatalf("daily: %q", lines)
	}
	out.Reset()
	if code := run([]string{"rpc"}, strings.NewReader(in), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	lines = strings.Split(strings.TrimSpace(out.String()), "\n")
	if !strings.Contains(lines[2], `"secret":"tok-1"`) || !strings.Contains(lines[3], `"exit":0`) {
		t.Fatalf("admin: %q", lines)
	}
	audit, _ := os.ReadFile(filepath.Join(home, ".local/state/valise/audit.jsonl"))
	if strings.Contains(string(audit), "tok-1") || strings.Count(string(audit), `"op":"action"`) != 4 {
		t.Fatalf("audit: %s", audit)
	}
}
