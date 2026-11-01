package ssh

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
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

	// ConfigFile replaces the user's ssh_config (-F): Valise's own file for
	// servers added by hand.
	ConfigFile string
	// Identity is the only key offered (-i with IdentitiesOnly=yes).
	Identity string
	// IdentityAgent is the agent socket, or "none".
	IdentityAgent string
	// NoPubkey disables public-key authentication (temporary password
	// login, so no key on disk or in an agent is tried).
	NoPubkey bool
	// BatchMode forbids interactive prompts (the daily key must never fall
	// back to asking for a password).
	BatchMode bool
	// AuthNone tries only the "none" method: logging in with no credential
	// at all (a probe for servers whose gateway lets anyone in).
	AuthNone bool
	// ControlPath is a master socket owned by one operation (closed by
	// CloseMaster, or after OperationPersistSeconds idle); it overrides
	// ControlDir.
	ControlPath string
	// NoControl disables connection sharing entirely.
	NoControl bool
	// ControlName prefixes Valise's shared master socket ("cm-" by
	// default). Masters are keyed only by host, port and user, so each
	// access level needs its own prefix: a daily-key master must never be
	// reused by a full login, or the other way round.
	ControlName string
}

// ControlPersistSeconds keeps Valise's master connection alive between calls.
const ControlPersistSeconds = 60

// OperationPersistSeconds bounds an operation's own master connection when
// it is idle, in case CloseMaster is never reached.
const OperationPersistSeconds = 120

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
const maxControlDirLen = 104 - len("/cmd-") - 40 - 17 - 1

// controlDir returns the directory for Valise's own ControlMaster sockets:
// the configured one if short enough, else /tmp/valise-<uid> when it is a
// private directory owned by us, else "" (no connection sharing).
func (t *Transport) controlDir() string {
	dir := t.opts.ControlDir
	if dir == "" || t.opts.NoControl || t.opts.ControlPath != "" || t.ReusesUserControlMaster() {
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

func (t *Transport) controlName() string {
	if t.opts.ControlName != "" {
		return t.opts.ControlName
	}
	return "cm-"
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

// CloseMaster ends the operation's own master connection (ControlPath),
// if any.
func (t *Transport) CloseMaster() {
	if t.opts.ControlPath == "" {
		return
	}
	_ = exec.Command(t.opts.Binary, append(t.base("-O", "exit"), "--", t.host)...).Run()
}

// Binary is the ssh executable.
func (t *Transport) Binary() string { return t.opts.Binary }

func (t *Transport) base(first ...string) []string {
	args := append([]string{}, first...)
	if t.opts.ConfigFile != "" {
		args = append(args, "-F", t.opts.ConfigFile)
	}
	if t.opts.Identity != "" {
		args = append(args, "-i", t.opts.Identity, "-o", "IdentitiesOnly=yes")
	}
	if t.opts.IdentityAgent != "" {
		args = append(args, "-o", "IdentityAgent="+t.opts.IdentityAgent)
	}
	if t.opts.NoPubkey {
		args = append(args, "-o", "PubkeyAuthentication=no", "-o", "IdentitiesOnly=yes")
	}
	if t.opts.BatchMode {
		args = append(args, "-o", "BatchMode=yes")
	}
	if t.opts.AuthNone {
		args = append(args, "-o", "PreferredAuthentications=none", "-o", "BatchMode=yes")
	}
	switch {
	case t.opts.NoControl:
		args = append(args, "-o", "ControlPath=none")
	case t.opts.ControlPath != "":
		args = append(args,
			"-o", "ControlMaster=auto",
			"-o", "ControlPath="+t.opts.ControlPath,
			"-o", fmt.Sprintf("ControlPersist=%d", OperationPersistSeconds),
		)
	}
	if dir := t.controlDir(); dir != "" {
		args = append(args,
			"-o", "ControlMaster=auto",
			"-o", "ControlPath="+filepath.Join(dir, t.controlName()+"%C"),
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
	switch {
	case strings.Contains(stderr, "REMOTE HOST IDENTIFICATION HAS CHANGED") || strings.Contains(stderr, "Host key verification failed"):
		return valise.New("host_key_mismatch",
			fmt.Sprintf("服务器 %s 的身份与第一次连接时不一致，已停止连接", t.host),
			"可能是服务器重装了系统，也可能是有人冒充服务器。先在服务商控制台核对主机密钥指纹；确认是重装后，在服务器设置里重新确认服务器身份").WithDetail(lastLines(stderr, 5))
	case t.opts.ConfigFile != "" && strings.Contains(stderr, "Permission denied"):
		return valise.New("ssh_auth_failed",
			fmt.Sprintf("服务器 %s 拒绝了登录", t.host),
			"日常钥匙可能已从服务器上移除，或这台电脑还没有它（换电脑、导入配置后），也可能是密码不对。重新装日常钥匙：valise server rekey "+t.host+"（终端界面：服务器设置里按 K），需要用服务器密码登录一次").WithDetail(lastLines(stderr, 5))
	}
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

// SocketPath returns a fresh path for a socket of one Valise process (an
// operation's master connection, the admin agent) in dir, or in
// /tmp/valise-<uid> when dir is too long for a Unix socket. It returns ""
// when neither is usable.
func SocketPath(dir, prefix string) string {
	if dir == "" || len(dir) > maxControlDirLen {
		dir = privateTmpDir()
	} else if os.MkdirAll(dir, 0o700) != nil {
		dir = privateTmpDir()
	}
	if dir == "" {
		return ""
	}
	var b [6]byte
	_, _ = rand.Read(b[:])
	return filepath.Join(dir, prefix+hex.EncodeToString(b[:]))
}
