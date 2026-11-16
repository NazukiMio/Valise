package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"valise/testbed/fakesshenv"
	"valise/ui/service"
)

func runCLI(t *testing.T, configDir, stdin string, args ...string) (string, string, int) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(context.Background(), append([]string{"--config-dir", configDir}, args...), strings.NewReader(stdin), &out, &errb)
	return out.String(), errb.String(), code
}

func TestCLIDoctorAndDeploy(t *testing.T) {
	e := fakesshenv.New(t)
	home := e.AddHost("box")
	cfg := t.TempDir()
	os.WriteFile(filepath.Join(cfg, "config.json"), []byte(`{"ssh_binary":"`+e.SSH+`"}`), 0o600)

	out, errOut, code := runCLI(t, cfg, "", "doctor", "box")
	if code != 0 || !strings.Contains(out, "porter：未部署") || !strings.Contains(errOut, "valise porter deploy box") {
		t.Fatalf("code=%d\n%s\n%s", code, out, errOut)
	}
	_, errOut, code = runCLI(t, cfg, "", "porter", "deploy", "box")
	if code != 1 || !strings.Contains(errOut, "--release") {
		t.Fatalf("code=%d %s", code, errOut)
	}
	out, errOut, code = runCLI(t, cfg, "", "porter", "deploy", "box", "--local", e.Porter())
	porterPath := filepath.Join(home, ".local/bin/valise-porter")
	if code != 0 || !strings.Contains(out, porterPath) {
		t.Fatalf("code=%d\n%s\n%s", code, out, errOut)
	}
	out, _, code = runCLI(t, cfg, "", "doctor", "box", "--json")
	var r service.DoctorReport
	if code != 0 || json.Unmarshal([]byte(out), &r) != nil || r.PorterPath != porterPath || !r.PorterFound {
		t.Fatalf("code=%d %s", code, out)
	}
	out, _, code = runCLI(t, cfg, "", "doctor", "box")
	for _, want := range []string{"系统与架构：", "porter：" + porterPath, "可用工具：", "未完成的交换：无"} {
		if code != 0 || !strings.Contains(out, want) {
			t.Fatalf("missing %q in\n%s", want, out)
		}
	}
}

func TestCLIConnectFailure(t *testing.T) {
	e := fakesshenv.New(t)
	e.AddHost("box")
	cfg := t.TempDir()
	os.WriteFile(filepath.Join(cfg, "config.json"), []byte(`{"ssh_binary":"`+e.SSH+`"}`), 0o600)
	t.Setenv("FAKESSH_FAIL_CONNECT", "box")
	_, errOut, code := runCLI(t, cfg, "", "doctor", "box")
	if code != 1 || !strings.Contains(errOut, "失败：无法连接到服务器 box") || !strings.Contains(errOut, "建议：") || strings.Contains(errOut, "Connection refused") {
		t.Fatalf("code=%d %s", code, errOut)
	}
	_, errOut, _ = runCLI(t, cfg, "", "-v", "doctor", "box")
	if !strings.Contains(errOut, "Connection refused") {
		t.Fatalf("-v must show detail: %s", errOut)
	}
}

func TestCLIKeys(t *testing.T) {
	cfg := t.TempDir()
	const pass = "twelve chars or more\n"
	out, _, code := runCLI(t, cfg, pass, "key", "init")
	if code != 0 || !strings.Contains(out, "指纹：") {
		t.Fatalf("code=%d %s", code, out)
	}
	show, _, _ := runCLI(t, cfg, "", "key", "show")
	kit := filepath.Join(t.TempDir(), "kit")
	if _, errOut, code := runCLI(t, cfg, "", "key", "export-kit", kit); code != 0 {
		t.Fatal(errOut)
	}
	other := t.TempDir()
	out, errOut, code := runCLI(t, other, pass, "key", "import-kit", kit)
	if code != 0 || !strings.Contains(out, strings.Split(show, "\n")[1]) {
		t.Fatalf("code=%d %s %s", code, out, errOut)
	}
	_, errOut, code = runCLI(t, t.TempDir(), "short\n", "key", "init")
	if code != 1 || !strings.Contains(errOut, "12") {
		t.Fatalf("code=%d %s", code, errOut)
	}
}

func TestCLIUsage(t *testing.T) {
	for _, args := range [][]string{{}, {"nope"}, {"doctor"}, {"doctor", "a", "b"}, {"key", "export-kit"}} {
		if _, _, code := runCLI(t, t.TempDir(), "", args...); code != 2 {
			t.Errorf("%q: code %d", args, code)
		}
	}
}

// `target remove` forgets a copy location on the server; `native
// --instance … --shell` is accepted only with an instance.
func TestCLITargetRemoveAndNativeShellUsage(t *testing.T) {
	e := fakesshenv.New(t)
	home := e.AddHost("box")
	e.InstallPorter("box")
	cfg := t.TempDir()
	os.WriteFile(filepath.Join(cfg, "config.json"), []byte(`{"ssh_binary":"`+e.SSH+`"}`), 0o600)
	targets := filepath.Join(home, ".config/valise/targets.json")
	os.MkdirAll(filepath.Dir(targets), 0o700)
	os.WriteFile(targets, []byte(`{"targets":[{"name":"old","kind":"rsync","dest":"sink:/backup/old"},{"name":"keep","kind":"rsync","dest":"sink:/backup/keep"}]}`), 0o600)

	out, errOut, code := runCLI(t, cfg, "", "target", "remove", "box", "old")
	if code != 0 || !strings.Contains(out, "已从 box 上移除备份位置 old") || !strings.Contains(out, "没有删除") {
		t.Fatalf("code=%d\n%s\n%s", code, out, errOut)
	}
	b, _ := os.ReadFile(targets)
	if strings.Contains(string(b), `"old"`) || !strings.Contains(string(b), `"keep"`) {
		t.Fatalf("targets.json: %s", b)
	}
	if _, _, code := runCLI(t, cfg, "", "target", "remove", "box"); code == 0 {
		t.Fatal("target remove without a name was accepted")
	}
	// --shell belongs to an instance; a profile-wide native has no shell.
	if _, _, code := runCLI(t, cfg, "", "native", "box", "--profile", "demo", "--shell"); code == 0 {
		t.Fatal("--shell without --instance was accepted")
	}
	out, _, _ = runCLI(t, cfg, "", "help")
	for _, want := range []string{"native <服务器> --instance <id> [--shell]", "target remove <服务器> <名称>"} {
		if !strings.Contains(out, want) {
			_, errOut, _ := runCLI(t, cfg, "")
			if !strings.Contains(errOut+out, want) {
				t.Errorf("usage lacks %q", want)
			}
		}
	}
}
