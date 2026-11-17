package service

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
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
	var mu sync.Mutex
	var asked []string
	answer, cancel := "s3cret pass", false
	s := &Service{PasswordAuth: PasswordAuth{Prompt: func(p string) (string, error) {
		mu.Lock()
		asked = append(asked, p)
		c := cancel
		mu.Unlock()
		if c {
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
	mu.Lock()
	gotAsked := append([]string{}, asked...)
	mu.Unlock()
	if len(gotAsked) != 1 || gotAsked[0] != "agent@192.0.2.24's password:" {
		t.Fatalf("prompt seen: %q", gotAsked)
	}
	mu.Lock()
	cancel = true
	mu.Unlock()
	if out, err := runHelper(t, env, "password:"); err == nil || out != "" {
		t.Fatalf("cancel answered %q", out)
	}
	mu.Lock()
	cancel = false
	mu.Unlock()
	var bad []string
	for _, kv := range env {
		if strings.HasPrefix(kv, askpassToken+"=") {
			kv = askpassToken + "=wrong"
		}
		bad = append(bad, kv)
	}
	out, err = runHelper(t, bad, "password:")
	mu.Lock()
	askedCount := len(asked)
	mu.Unlock()
	if err == nil || out != "" || askedCount != 2 {
		t.Fatalf("wrong token answered %q (asked %d times)", out, askedCount)
	}
}
