package ssh

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"valise/core/valise"
)

// Options configure how ssh(1) is run.
type Options struct {
	// Binary is the ssh executable, "ssh" by default (config ssh_binary).
	Binary string
	// ControlDir holds Valise's own ControlMaster sockets. Empty disables
	// connection sharing unless the user configured it.
	ControlDir string
	// HostConfig is the user's resolved ssh_config for the host. When it
	// sets a ControlPath, the user's connection sharing is reused.
	HostConfig *HostConfig
}

// ControlPersistSeconds keeps Valise's master connection alive between calls.
const ControlPersistSeconds = 60

// Transport runs commands on one host through the system OpenSSH.
type Transport struct {
	host string
	opts Options
}

// ErrBadHost is returned for aliases that could be read as ssh options.
var ErrBadHost = errors.New("服务器别名无效")

// New returns a Transport for host, an alias or user@host as typed in ssh.
func New(host string, opts Options) (*Transport, error) {
	if host == "" || strings.HasPrefix(host, "-") || strings.ContainsAny(host, " \t\n") {
		return nil, ErrBadHost
	}
	if opts.Binary == "" {
		opts.Binary = "ssh"
	}
	return &Transport{host: host, opts: opts}, nil
}

// Host returns the host alias.
func (t *Transport) Host() string { return t.host }

// ReusesUserControlMaster reports whether the user's own connection sharing
// is used instead of Valise's.
func (t *Transport) ReusesUserControlMaster() bool {
	return t.opts.HostConfig != nil && t.opts.HostConfig.UserControlMaster()
}

// maxControlDirLen keeps "<dir>/cm-<40 hex>" plus the 17-byte temporary
// suffix OpenSSH appends while creating the master under the 104-byte
// sun_path limit (macOS; Linux allows 108).
const maxControlDirLen = 104 - len("/cm-") - 40 - 17 - 1

// controlDir returns the directory for Valise's own ControlMaster sockets:
// the configured one if short enough, else /tmp/valise-<uid> when it is a
// private directory owned by us, else "" (no connection sharing).
func (t *Transport) controlDir() string {
	dir := t.opts.ControlDir
	if dir == "" || t.ReusesUserControlMaster() {
		return ""
	}
	if len(dir) <= maxControlDirLen {
		if os.MkdirAll(dir, 0o700) == nil {
			return dir
		}
		return ""
	}
	return privateTmpDir()
}

// Args builds the ssh argument list for a remote command.
func (t *Transport) Args(remoteCmd string) []string {
	return append(t.base("-T"), "--", t.host, remoteCmd)
}

// TTYArgs runs remoteCmd under a remote PTY (ssh -tt), for interactive
// login commands.
func (t *Transport) TTYArgs(remoteCmd string) []string {
	return append(t.base("-tt"), "--", t.host, remoteCmd)
}

// ShellArgs opens an interactive login shell on the server (ssh -t, no
// remote command).
func (t *Transport) ShellArgs() []string {
	return append(t.base("-t"), "--", t.host)
}

// ForwardArgs connects stdin/stdout to host:port as seen from the server
// (ssh -W), one connection of a tunnel.
func (t *Transport) ForwardArgs(host string, port int) []string {
	return append(t.base("-W", net.JoinHostPort(host, strconv.Itoa(port))), "--", t.host)
}

// Binary is the ssh executable.
func (t *Transport) Binary() string { return t.opts.Binary }

func (t *Transport) base(first ...string) []string {
	args := append([]string{}, first...)
	if dir := t.controlDir(); dir != "" {
		args = append(args,
			"-o", "ControlMaster=auto",
			"-o", "ControlPath="+filepath.Join(dir, "cm-%C"),
			"-o", fmt.Sprintf("ControlPersist=%d", ControlPersistSeconds),
		)
	}
	return args
}

// Command returns an unstarted ssh command for remoteCmd.
func (t *Transport) Command(remoteCmd string) (*exec.Cmd, error) {
	return exec.Command(t.opts.Binary, t.Args(remoteCmd)...), nil
}

// Result is the outcome of a one-shot remote command.
type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// ExitConnFailed is ssh(1)'s exit status for connection errors.
const ExitConnFailed = 255

// Run executes remoteCmd once. A non-zero remote exit is reported in Result,
// not as an error; exit 255 (connection failure) is a UserError.
func (t *Transport) Run(ctx context.Context, remoteCmd string, stdin io.Reader) (*Result, error) {
	cmd, err := t.Command(remoteCmd)
	if err != nil {
		return nil, err
	}
	var out, errb bytes.Buffer
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, &out, &errb
	if err := runContext(ctx, cmd); err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			return nil, t.startError(err)
		}
	}
	res := &Result{Stdout: out.Bytes(), Stderr: errb.Bytes(), ExitCode: cmd.ProcessState.ExitCode()}
	if res.ExitCode == ExitConnFailed {
		return res, t.ConnError(errb.String())
	}
	return res, nil
}

// runContext runs cmd and kills it when ctx ends.
func runContext(ctx context.Context, cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = cmd.Process.Kill()
		case <-done:
		}
	}()
	err := cmd.Wait()
	close(done)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

// ConnError is the user-facing error for a failed SSH connection.
func (t *Transport) ConnError(stderr string) *valise.UserError {
	return valise.New("ssh_connect_failed",
		fmt.Sprintf("无法连接到服务器 %s", t.host),
		fmt.Sprintf("先在终端中确认 `ssh %s` 可以登录，再检查网络、主机名和 SSH 密钥（Valise 不保存登录凭据，全部交给系统 OpenSSH）", t.host),
	).WithDetail(lastLines(stderr, 5))
}

func (t *Transport) startError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return valise.New("ssh_not_runnable",
		fmt.Sprintf("无法运行 SSH 客户端 %q", t.opts.Binary),
		"确认系统已安装 OpenSSH，或在 config.json 中把 ssh_binary 指向正确的路径",
	).WithDetail(err.Error())
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// Quote returns s as a single POSIX shell word.
func Quote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./=:@%+,") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
