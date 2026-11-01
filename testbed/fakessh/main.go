//go:build unix

// Command fakessh impersonates ssh(1) for tests. It never touches the
// network: each host alias maps to a fake HOME under $FAKESSH_ROOT, and the
// remote command runs there with `sh -c`. See AGENT.md for the contract.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

var logf = func(string, ...any) {}

func run(args []string) int {
	if p := os.Getenv("FAKESSH_LOG"); p != "" {
		if f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
			defer f.Close()
			logf = func(format string, a ...any) { fmt.Fprintf(f, "fakessh: "+format+"\n", a...) }
		}
	}
	logf("argv %q", args)
	inv, err := parseArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ssh:", err)
		return 255
	}
	for _, ig := range inv.ignored {
		logf("ignored option %s", ig)
	}
	logf("alias=%s tty=%d options=%q port=%q user=%q config=%q", inv.alias, inv.tty, inv.options, inv.port, inv.user, inv.config)

	root := os.Getenv("FAKESSH_ROOT")
	if root == "" {
		fmt.Fprintln(os.Stderr, "fakessh: FAKESSH_ROOT is not set")
		return 255
	}
	if ms, err := strconv.Atoi(os.Getenv("FAKESSH_DELAY_MS")); err == nil && ms > 0 {
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}
	for _, a := range strings.Split(os.Getenv("FAKESSH_FAIL_CONNECT"), ",") {
		if a != "" && a == inv.alias {
			fmt.Fprintf(os.Stderr, "ssh: connect to host %s port 22: Connection refused\n", inv.alias)
			return 255
		}
	}
	home := filepath.Join(root, "hosts", inv.alias, "home")
	if inv.alias == "" || inv.alias != filepath.Base(inv.alias) || strings.HasPrefix(inv.alias, ".") {
		home = ""
	}
	if st, err := os.Stat(home); home == "" || err != nil || !st.IsDir() {
		fmt.Fprintf(os.Stderr, "ssh: Could not resolve hostname %s: Name or service not known\n", inv.alias)
		return 255
	}
	if inv.ctl != "" {
		return 0 // master control (-O exit): there is no master to stop
	}
	command, env := inv.command, remoteEnv(home)
	if inv.authNone() && !listed(os.Getenv("FAKESSH_AUTH_NONE"), inv.alias) {
		fmt.Fprintf(os.Stderr, "%s@%s: Permission denied (publickey,password).\n", inv.user, inv.alias)
		return 255
	}
	if inv.identity != "" && !inv.noPubkey() && !listed(os.Getenv("FAKESSH_IGNORE_KEYS"), inv.alias) {
		forced, ok := authorize(home, inv.identity)
		if !ok {
			fmt.Fprintf(os.Stderr, "%s@%s: Permission denied (publickey).\n", inv.user, inv.alias)
			return 255
		}
		if forced != "" {
			env = append(env, "SSH_ORIGINAL_COMMAND="+command)
			command = forced
		}
	}
	if command == "" {
		fmt.Fprintln(os.Stderr, "fakessh: interactive sessions are not supported")
		return 255
	}

	cmd := exec.Command("sh", "-c", command)
	cmd.Dir = home
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	drop, _ := strconv.ParseInt(os.Getenv("FAKESSH_DROP_AFTER_BYTES"), 10, 64)
	if drop <= 0 {
		cmd.Stdout = os.Stdout
		return exitCode(cmd.Run())
	}
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Fprintln(os.Stderr, "fakessh:", err)
		return 255
	}
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "fakessh:", err)
		return 255
	}
	n, _ := io.CopyN(os.Stdout, pipe, drop)
	if n < drop {
		return exitCode(cmd.Wait())
	}
	logf("dropping connection after %d bytes", n)
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	_ = cmd.Wait()
	fmt.Fprintf(os.Stderr, "Connection to %s closed by remote host.\n", inv.alias)
	return 255
}

func listed(csv, alias string) bool {
	for _, a := range strings.Split(csv, ",") {
		if a != "" && a == alias {
			return true
		}
	}
	return false
}

// remoteEnv is the caller's environment with HOME and PWD pointing at the
// fake home and every FAKESSH_* variable removed.
func remoteEnv(home string) []string {
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if k == "HOME" || k == "PWD" || strings.HasPrefix(k, "FAKESSH_") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "HOME="+home, "PWD="+home)
}

// exitCode mirrors OpenSSH: the remote exit status, or 255 when the remote
// command died from a signal or could not start.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ee.ExitCode() >= 0 {
			return ee.ExitCode()
		}
		return 255
	}
	fmt.Fprintln(os.Stderr, "fakessh:", err)
	return 255
}
