package service

import (
	"bufio"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// The desktop's password relay (docs/ARCHITECTURE.md 2.1): OpenSSH runs
// this same executable as its askpass helper in relay mode; the helper
// hands OpenSSH's prompt to the window through a socket only this process
// listens on, and hands the answer back. The answer passes through memory
// only.

const (
	askpassRelay = "relay"
	askpassSock  = "VALISE_ASKPASS_SOCK"
	askpassToken = "VALISE_ASKPASS_TOKEN"
)

// PasswordAuth is set by a frontend without a terminal to answer
// OpenSSH's own prompts (a server password, a key's passphrase).
type PasswordAuth struct {
	// Prompt shows OpenSSH's prompt and returns what the user typed; an
	// error means the user cancelled.
	Prompt func(prompt string) (string, error)
}

type relayState struct {
	once  sync.Once
	sock  string
	token string
	err   error
}

// canPrompt reports whether OpenSSH may ask through the frontend.
func (s *Service) canPrompt() bool { return s.PasswordAuth.Prompt != nil }

// relayEnv starts the relay once and returns the environment an ssh that
// may prompt needs.
func (s *Service) relayEnv() ([]string, error) {
	r := &s.askRelay
	r.once.Do(func() { r.err = s.startRelay() })
	if r.err != nil {
		return nil, r.err
	}
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return []string{"SSH_ASKPASS=" + self, "SSH_ASKPASS_REQUIRE=force", askpassEnv + "=" + askpassRelay,
		askpassSock + "=" + r.sock, askpassToken + "=" + r.token}, nil
}

func (s *Service) startRelay() error {
	dir, err := os.MkdirTemp(shortTmp(), "valise-ap-")
	if err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return err
	}
	r := &s.askRelay
	r.sock, r.token = filepath.Join(dir, "s"), hex.EncodeToString(b[:])
	l, err := net.Listen("unix", r.sock)
	if err != nil {
		return err
	}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go s.answer(c)
		}
	}()
	return nil
}

// shortTmp is a directory short enough for a socket path (104 bytes on
// macOS); the socket's own directory is private either way.
func shortTmp() string {
	if d := os.TempDir(); len(d) < 60 {
		return d
	}
	return "/tmp"
}

// answer serves one helper: its token, then OpenSSH's prompt; it gets
// "ok:" and the answer, or "no" when the user cancelled.
func (s *Service) answer(c net.Conn) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(15 * time.Minute))
	rd := bufio.NewReader(c)
	token, err := rd.ReadString('\n')
	if err != nil || subtle.ConstantTimeCompare([]byte(strings.TrimSpace(token)), []byte(s.askRelay.token)) != 1 {
		return
	}
	prompt, _ := rd.ReadString('\n')
	prompt = strings.ReplaceAll(strings.TrimSpace(prompt), "\\n", "\n")
	s.logAskpass(prompt)
	if s.PasswordAuth.Prompt == nil {
		fmt.Fprintln(c, "no")
		return
	}
	v, err := s.PasswordAuth.Prompt(prompt)
	if err != nil || strings.ContainsAny(v, "\r\n") {
		fmt.Fprintln(c, "no")
		return
	}
	fmt.Fprintln(c, "ok:"+v)
}

// relayAsk is the helper's side: run by OpenSSH with its prompt as the
// argument, it prints the answer, or fails so OpenSSH gives up.
func relayAsk() {
	prompt := strings.ReplaceAll(strings.Join(os.Args[1:], " "), "\n", "\\n")
	c, err := net.Dial("unix", os.Getenv(askpassSock))
	if err != nil {
		os.Exit(1)
	}
	defer c.Close()
	fmt.Fprintln(c, os.Getenv(askpassToken))
	fmt.Fprintln(c, prompt)
	line, err := bufio.NewReader(c).ReadString('\n')
	v, ok := strings.CutPrefix(strings.TrimRight(line, "\r\n"), "ok:")
	if err != nil || !ok {
		os.Exit(1)
	}
	fmt.Println(v)
	os.Exit(0)
}
