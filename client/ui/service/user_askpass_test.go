package service

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"

	"valise/client/config"
)

// TestUserTransportEncryptedKeyAskpass verifies with a real OpenSSH client
// (`ssh`) and a real passphrase-protected key (`ssh-keygen -N`) that
// userTransport for ~/.ssh/config hosts clears BatchMode and attaches
// relayEnv() when PasswordAuth.Prompt is set, passing OpenSSH's passphrase
// prompt through SSH_ASKPASS without touching the user's ~/.ssh directory.
func TestUserTransportEncryptedKeyAskpass(t *testing.T) {
	for _, tool := range []string{"ssh", "ssh-keygen"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not installed")
		}
	}
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "id_ed25519")
	const keyPass = "user-key-passphrase-42"
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", keyPass, "-f", keyPath).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen user key: %v: %s", err, out)
	}
	userPubBytes, err := os.ReadFile(keyPath + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	userPub, _, _, _, err := gossh.ParseAuthorizedKey(userPubBytes)
	if err != nil {
		t.Fatal(err)
	}

	_, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostSigner, err := gossh.NewSignerFromKey(hostPriv)
	if err != nil {
		t.Fatal(err)
	}

	var accepted bool
	var mu sync.Mutex
	srvCfg := &gossh.ServerConfig{
		PublicKeyCallback: func(_ gossh.ConnMetadata, k gossh.PublicKey) (*gossh.Permissions, error) {
			if string(k.Marshal()) == string(userPub.Marshal()) {
				mu.Lock()
				accepted = true
				mu.Unlock()
				return &gossh.Permissions{}, nil
			}
			return nil, errors.New("unknown key")
		},
		PasswordCallback: func(_ gossh.ConnMetadata, pw []byte) (*gossh.Permissions, error) {
			if string(pw) == "ssh-pw-99" {
				return &gossh.Permissions{}, nil
			}
			return nil, errors.New("bad password")
		},
	}
	srvCfg.AddHostKey(hostSigner)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("loopback listen unavailable: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go serveOneSSHExec(c, srvCfg)
		}
	}()

	knownHosts := filepath.Join(dir, "known_hosts")
	khLine := fmt.Sprintf("[127.0.0.1]:%d %s", port, gossh.MarshalAuthorizedKey(hostSigner.PublicKey()))
	if err := os.WriteFile(knownHosts, []byte(khLine), 0o600); err != nil {
		t.Fatal(err)
	}
	sshCfgPath := filepath.Join(dir, "ssh_config")
	cfgBody := fmt.Sprintf(
		"Host realbox\n  HostName 127.0.0.1\n  Port %d\n  User tester\n  IdentityFile %s\n  IdentitiesOnly yes\n  IdentityAgent none\n  UserKnownHostsFile %s\n  StrictHostKeyChecking yes\n"+
			"Host realbox-cancel\n  HostName 127.0.0.1\n  Port %d\n  User canceler\n  IdentityFile %s\n  IdentitiesOnly yes\n  IdentityAgent none\n  UserKnownHostsFile %s\n  StrictHostKeyChecking yes\n"+
			"Host realbox-pw\n  HostName 127.0.0.1\n  Port %d\n  User pwtrial\n  PubkeyAuthentication no\n  UserKnownHostsFile %s\n  StrictHostKeyChecking yes\n",
		port, keyPath, knownHosts, port, keyPath, knownHosts, port, knownHosts,
	)
	if err := os.WriteFile(sshCfgPath, []byte(cfgBody), 0o600); err != nil {
		t.Fatal(err)
	}
	realSSH, err := exec.LookPath("ssh")
	if err != nil {
		t.Fatal(err)
	}
	sshWrapper := filepath.Join(dir, "ssh-wrap")
	wrapperBody := fmt.Sprintf("#!/bin/sh\nexec %q -F %q \"$@\"\n", realSSH, sshCfgPath)
	if err := os.WriteFile(sshWrapper, []byte(wrapperBody), 0o700); err != nil {
		t.Fatal(err)
	}

	// 1. Without PasswordAuth.Prompt and NoTerminalLogin=true, BatchMode=yes
	// blocks prompting and refuses the encrypted key.
	sNoPrompt := &Service{
		Dirs:            config.Dirs{Data: filepath.Join(dir, "d1"), Cache: filepath.Join(dir, "c1")},
		Config:          config.Config{SSHBinary: sshWrapper},
		Home:            dir,
		SSHConfigPath:   sshCfgPath,
		NoTerminalLogin: true,
	}
	tr, _, err := sNoPrompt.userTransport("realbox")
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range tr.Args("echo ok") {
		if a == "BatchMode=yes" {
			goto hasBatch
		}
	}
	t.Fatalf("expected BatchMode=yes when canPrompt is false: %v", tr.Args("echo ok"))
hasBatch:
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := tr.Run(ctx, "echo ok", nil); err == nil {
		t.Fatal("expected failure without askpass prompt")
	}

	// 2. With PasswordAuth.Prompt set, BatchMode=yes is omitted and
	// relayEnv() delivers OpenSSH's passphrase prompt to the frontend.
	var prompts []string
	var pmu sync.Mutex
	s := &Service{
		Dirs:            config.Dirs{Data: filepath.Join(dir, "d2"), Cache: filepath.Join(dir, "c2")},
		Config:          config.Config{SSHBinary: sshWrapper},
		Home:            dir,
		SSHConfigPath:   sshCfgPath,
		NoTerminalLogin: true,
		PasswordAuth: PasswordAuth{Prompt: func(p string) (string, error) {
			pmu.Lock()
			prompts = append(prompts, p)
			pmu.Unlock()
			return keyPass, nil
		}},
	}
	t.Cleanup(s.Close)

	tr2, _, err := s.userTransport("realbox")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		args := tr2.Args("x")
		for i, a := range args {
			if strings.HasPrefix(a, "ControlPath=") {
				_ = exec.Command(sshWrapper, "-o", a, "-O", "exit", "realbox").Run()
				_ = i
			}
		}
	})
	for _, a := range tr2.Args("echo ok") {
		if a == "BatchMode=yes" {
			t.Fatalf("BatchMode=yes must not be set when canPrompt is true: %v", tr2.Args("echo ok"))
		}
	}
	res, err := tr2.Run(ctx, "echo ok", nil)
	if err != nil {
		t.Fatalf("userTransport with askpass failed: %v", err)
	}
	if res.ExitCode != 0 || strings.TrimSpace(string(res.Stdout)) != "hello-askpass" {
		t.Fatalf("unexpected result: code=%d stdout=%q stderr=%q", res.ExitCode, res.Stdout, res.Stderr)
	}
	pmu.Lock()
	gotPrompts := append([]string{}, prompts...)
	pmu.Unlock()
	if len(gotPrompts) == 0 || !strings.Contains(strings.ToLower(gotPrompts[0]), "passphrase") {
		t.Fatalf("expected OpenSSH passphrase prompt, got %q", gotPrompts)
	}
	mu.Lock()
	okAccepted := accepted
	mu.Unlock()
	if !okAccepted {
		t.Fatal("server never saw public key authentication")
	}

	// 3. User cancelling the passphrase dialog aborts cleanly without hanging.
	sCancel := &Service{
		Dirs:            config.Dirs{Data: filepath.Join(dir, "d3"), Cache: filepath.Join(dir, "c3")},
		Config:          config.Config{SSHBinary: sshWrapper},
		Home:            dir,
		SSHConfigPath:   sshCfgPath,
		NoTerminalLogin: true,
		PasswordAuth:    PasswordAuth{Prompt: func(string) (string, error) { return "", errors.New("cancelled") }},
	}
	t.Cleanup(sCancel.Close)
	tr3, _, err := sCancel.userTransport("realbox-cancel")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tr3.Run(ctx, "echo ok", nil); err == nil {
		t.Fatal("expected cancelled askpass to fail authentication")
	}

	// 4. Password authentication via ~/.ssh/config host also invokes askpass.
	var pwPrompt string
	sPW := &Service{
		Dirs:            config.Dirs{Data: filepath.Join(dir, "d4"), Cache: filepath.Join(dir, "c4")},
		Config:          config.Config{SSHBinary: sshWrapper},
		Home:            dir,
		SSHConfigPath:   sshCfgPath,
		NoTerminalLogin: true,
		PasswordAuth: PasswordAuth{Prompt: func(p string) (string, error) {
			pwPrompt = p
			return "ssh-pw-99", nil
		}},
	}
	t.Cleanup(sPW.Close)
	tr4, _, err := sPW.userTransport("realbox-pw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, a := range tr4.Args("x") {
			if strings.HasPrefix(a, "ControlPath=") {
				_ = exec.Command(sshWrapper, "-o", a, "-O", "exit", "realbox-pw").Run()
			}
		}
	})
	res4, err := tr4.Run(ctx, "echo ok", nil)
	if err != nil || res4.ExitCode != 0 || strings.TrimSpace(string(res4.Stdout)) != "hello-askpass" {
		t.Fatalf("password login via userTransport failed: err=%v res=%+v", err, res4)
	}
	if !strings.Contains(strings.ToLower(pwPrompt), "password") {
		t.Fatalf("expected OpenSSH password prompt, got %q", pwPrompt)
	}
}

func serveOneSSHExec(nc net.Conn, cfg *gossh.ServerConfig) {
	defer nc.Close()
	_, chans, reqs, err := gossh.NewServerConn(nc, cfg)
	if err != nil {
		return
	}
	go gossh.DiscardRequests(reqs)
	for newChan := range chans {
		if newChan.ChannelType() != "session" {
			_ = newChan.Reject(gossh.UnknownChannelType, "unsupported")
			continue
		}
		ch, reqs, err := newChan.Accept()
		if err != nil {
			return
		}
		go func() {
			defer ch.Close()
			for req := range reqs {
				if req.Type == "exec" {
					if req.WantReply {
						_ = req.Reply(true, nil)
					}
					_, _ = ch.Write([]byte("hello-askpass\n"))
					var status [4]byte
					binary.BigEndian.PutUint32(status[:], 0)
					_, _ = ch.SendRequest("exit-status", false, status[:])
					return
				}
				if req.WantReply {
					_ = req.Reply(false, nil)
				}
			}
		}()
	}
}
