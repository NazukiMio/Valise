package fakesshenv

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runSSH(t *testing.T, e *Env, stdin string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(e.SSH, args...)
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return out.String(), errb.String(), code
}

func TestFakeSSHBasics(t *testing.T) {
	e := New(t)
	home := e.AddHost("box")
	log := filepath.Join(t.TempDir(), "log")
	t.Setenv("FAKESSH_LOG", log)
	t.Setenv("FAKESSH_SECRET_PROBE", "x")

	out, _, code := runSSH(t, e, "", "-T", "-v", "box", `printf '%s|%s|' "$HOME" "$PWD"; env | grep -c FAKESSH_ || true`)
	if code != 0 || out != home+"|"+home+"|0\n" {
		t.Fatalf("code=%d out=%q", code, out)
	}
	out, _, code = runSSH(t, e, "hello stdin", "box", "cat; exit 7")
	if code != 7 || out != "hello stdin" {
		t.Fatalf("code=%d out=%q", code, out)
	}
	b, _ := os.ReadFile(log)
	if !strings.Contains(string(b), "ignored option -v") {
		t.Fatalf("log = %s", b)
	}
}

func TestFakeSSHUnknownHost(t *testing.T) {
	e := New(t)
	for _, alias := range []string{"nohost", "..", "../x"} {
		_, errOut, code := runSSH(t, e, "", alias, "true")
		if code != 255 || !strings.Contains(errOut, "Could not resolve hostname") {
			t.Fatalf("%s: code=%d err=%q", alias, code, errOut)
		}
	}
}

func TestFakeSSHFailConnect(t *testing.T) {
	e := New(t)
	e.AddHost("box")
	t.Setenv("FAKESSH_FAIL_CONNECT", "box")
	_, errOut, code := runSSH(t, e, "", "box", "true")
	if code != 255 || !strings.Contains(errOut, "Connection refused") {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
}

func TestFakeSSHDropAfterBytes(t *testing.T) {
	e := New(t)
	e.AddHost("box")
	t.Setenv("FAKESSH_DROP_AFTER_BYTES", "5")
	out, errOut, code := runSSH(t, e, "", "box", "printf 0123456789; sleep 5")
	if code != 255 || out != "01234" || !strings.Contains(errOut, "closed by remote host") {
		t.Fatalf("code=%d out=%q err=%q", code, out, errOut)
	}
}

func TestInstallPorter(t *testing.T) {
	e := New(t)
	e.AddHost("box")
	e.InstallPorter("box")
	out, _, code := runSSH(t, e, "", "box", `"$HOME/.local/bin/valise-porter" version`)
	if code != 0 || !strings.HasPrefix(out, "valise-porter ") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}
