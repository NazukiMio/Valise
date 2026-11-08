package service

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// runHelper runs this test binary as OpenSSH would run the askpass helper.
func runHelper(t *testing.T, env []string, prompt string) (string, error) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self, prompt)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.Output()
	return string(out), err
}

// OpenSSH's prompt reaches the frontend and its answer reaches OpenSSH;
// a cancel makes the helper fail; a wrong token gets nothing.
func TestAskpassRelay(t *testing.T) {
	var asked []string
	answer, cancel := "s3cret pass", false
	s := &Service{PasswordAuth: PasswordAuth{Prompt: func(p string) (string, error) {
		asked = append(asked, p)
		if cancel {
			return "", errors.New("cancelled")
		}
		return answer, nil
	}}}
	env, err := s.relayEnv()
	if err != nil {
		t.Fatal(err)
	}
	out, err := runHelper(t, env, "agent@192.0.2.24's password: ")
	if err != nil || out != answer+"\n" {
		t.Fatalf("answer %q, %v", out, err)
	}
	if len(asked) != 1 || asked[0] != "agent@192.0.2.24's password:" {
		t.Fatalf("prompt seen: %q", asked)
	}
	cancel = true
	if out, err := runHelper(t, env, "password:"); err == nil || out != "" {
		t.Fatalf("cancel answered %q", out)
	}
	cancel = false
	var bad []string
	for _, kv := range env {
		if strings.HasPrefix(kv, askpassToken+"=") {
			kv = askpassToken + "=wrong"
		}
		bad = append(bad, kv)
	}
	if out, err := runHelper(t, bad, "password:"); err == nil || out != "" || len(asked) != 2 {
		t.Fatalf("wrong token answered %q (asked %d times)", out, len(asked))
	}
}
