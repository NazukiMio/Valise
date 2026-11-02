package service

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"valise/client/keystore"
	"valise/client/ssh"
	"valise/core/valise"
)

// Keys are generated and encrypted by OpenSSH (ssh-keygen); Valise only
// passes the passphrase through an askpass helper. The helper is this same
// executable: init below answers OpenSSH's prompt and exits before main.

const (
	askpassEnv    = "VALISE_ASKPASS"
	askpassSecret = "VALISE_ASKPASS_SECRET"
	askpassOld    = "VALISE_ASKPASS_OLD"
	askpassNew    = "VALISE_ASKPASS_NEW"
)

func init() {
	if os.Getenv(askpassEnv) != "1" {
		return
	}
	// Match only OpenSSH's own wording at the start: the rest of a prompt
	// can contain a key path.
	prompt := strings.ToLower(strings.TrimSpace(strings.Join(os.Args[1:], " ")))
	v := os.Getenv(askpassSecret)
	switch {
	case strings.HasPrefix(prompt, "bad passphrase"):
		v = "" // a wrong passphrase: answer empty so ssh-add gives up instead of asking forever
	case strings.HasPrefix(prompt, "enter old passphrase"):
		v = os.Getenv(askpassOld)
	case os.Getenv(askpassNew) != "":
		v = os.Getenv(askpassNew)
	}
	fmt.Println(v)
	os.Exit(0)
}

// askpassEnviron is the environment for an OpenSSH tool that reads a
// passphrase through the askpass helper. The secrets are visible only to
// processes of the same user, like the key files themselves.
func askpassEnviron(secrets map[string]string) ([]string, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	env := []string{}
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if k == "SSH_ASKPASS" || k == "SSH_ASKPASS_REQUIRE" || k == "DISPLAY" || strings.HasPrefix(k, "VALISE_ASKPASS") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "SSH_ASKPASS="+self, "SSH_ASKPASS_REQUIRE=force", askpassEnv+"=1")
	for k, v := range secrets {
		env = append(env, k+"="+v)
	}
	return env, nil
}

// toolPath finds an OpenSSH tool next to the configured ssh binary, else
// on PATH.
func (s *Service) toolPath(name string) string {
	if bin := s.Config.SSHBinary; strings.ContainsRune(bin, os.PathSeparator) {
		if p := filepath.Join(filepath.Dir(bin), name); fileExists(p) {
			return p
		}
	}
	return name
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

func runTool(ctx context.Context, path string, args []string, env []string) error {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = env
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %v: %s", filepath.Base(path), err, strings.TrimSpace(errb.String()))
	}
	return nil
}

// ensureDailyKey creates host's daily key (no passphrase) if missing.
func (s *Service) ensureDailyKey(ctx context.Context, host string) error {
	p := s.keyPath(host, dailyKeyName)
	if fileExists(p) && fileExists(p+".pub") {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	if err := runTool(ctx, s.toolPath("ssh-keygen"), []string{"-q", "-t", "ed25519", "-N", "", "-C", "valise-daily", "-f", p}, os.Environ()); err != nil {
		return valise.New("keygen_failed", "无法生成日常钥匙", "确认本机安装了 OpenSSH 的 ssh-keygen").WithDetail(err.Error())
	}
	return nil
}

// createAdminKey creates host's admin key, encrypted with the admin
// passphrase by ssh-keygen.
func (s *Service) createAdminKey(ctx context.Context, host, passphrase string) error {
	if err := s.checkAdminPassphrase(passphrase); err != nil {
		return err
	}
	p := s.keyPath(host, adminKeyName)
	if fileExists(p) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	env, err := askpassEnviron(map[string]string{askpassSecret: passphrase})
	if err != nil {
		return err
	}
	if err := runTool(ctx, s.toolPath("ssh-keygen"), []string{"-q", "-t", "ed25519", "-C", "valise-admin", "-f", p}, env); err != nil {
		return valise.New("keygen_failed", "无法生成管理钥匙", "确认本机安装了 OpenSSH 的 ssh-keygen").WithDetail(err.Error())
	}
	return nil
}

// checkAdminPassphrase enforces the length rule and that the admin
// passphrase differs from the Valise passphrase (docs/ARCHITECTURE.md 2.1).
func (s *Service) checkAdminPassphrase(passphrase string) error {
	if len([]rune(passphrase)) < 12 {
		return valise.New("admin_passphrase_short", "管理口令至少要 12 个字符", "换一个更长的口令")
	}
	if data, err := os.ReadFile(s.keystorePath()); err == nil {
		if k, err := keystore.Unlock(data, passphrase); err == nil {
			k.Lock()
			return valise.New("admin_passphrase_same", "管理口令不能和 Valise 口令相同", "两个口令保护的东西不同，请换一个")
		}
	}
	return nil
}

// ChangeAdminPassphrase re-encrypts every admin key with a new passphrase.
func (s *Service) ChangeAdminPassphrase(ctx context.Context, old, new string) (int, error) {
	if err := s.checkAdminPassphrase(new); err != nil {
		return 0, err
	}
	keys, _ := filepath.Glob(filepath.Join(s.Dirs.Data, "keys", "*", adminKeyName))
	env, err := askpassEnviron(map[string]string{askpassOld: old, askpassNew: new})
	if err != nil {
		return 0, err
	}
	for i, k := range keys {
		if err := runTool(ctx, s.toolPath("ssh-keygen"), []string{"-q", "-p", "-f", k}, env); err != nil {
			return i, valise.New("admin_passphrase_change_failed", "修改管理口令失败（"+filepath.Base(filepath.Dir(k))+"）",
				"确认旧口令正确；已修改的钥匙用新口令，其余仍用旧口令").WithDetail(err.Error())
		}
	}
	s.LockAdmin()
	return len(keys), nil
}

// adminAgent is Valise's own ssh-agent for unlocked admin keys. It is a
// child of this process and ends with it (Close).
type adminAgent struct {
	mu   sync.Mutex
	sock string
	cmd  *exec.Cmd
	// unlocked is when each host's admin key was added, for the remaining
	// window shown in the UI (the agent itself enforces the window).
	unlocked map[string]time.Time
}

// AdminWindow is how long an unlocked admin key stays in the agent.
func (s *Service) AdminWindow() time.Duration {
	m := s.Config.AdminWindowMinutes
	if m < 1 || m > 120 {
		m = 15
	}
	return time.Duration(m) * time.Minute
}

func (s *Service) agentSock(ctx context.Context) (string, error) {
	a := &s.agent
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cmd != nil && a.cmd.ProcessState == nil && fileExists(a.sock) {
		return a.sock, nil
	}
	sock := ssh.SocketPath(s.Dirs.Cache, "agent-")
	if sock == "" {
		return "", valise.New("agent_dir", "找不到放管理钥匙代理的私有目录", "检查缓存目录的权限")
	}
	cmd := exec.Command(s.toolPath("ssh-agent"), "-D", "-a", sock)
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		return "", valise.New("agent_start", "无法启动管理钥匙代理（ssh-agent）", "确认本机安装了 OpenSSH").WithDetail(err.Error())
	}
	go func() { _ = cmd.Wait() }()
	for i := 0; i < 50 && !fileExists(sock); i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if !fileExists(sock) {
		_ = cmd.Process.Kill()
		return "", valise.New("agent_start", "管理钥匙代理没有按时就绪", "重试一次")
	}
	a.sock, a.cmd = sock, cmd
	return sock, nil
}

// unlockAdmin makes sure host's admin key is in the agent, asking for the
// admin passphrase when it is not, and returns the agent socket.
func (s *Service) unlockAdmin(ctx context.Context, host string) (string, error) {
	sock, err := s.agentSock(ctx)
	if err != nil {
		return "", err
	}
	key := s.keyPath(host, adminKeyName)
	base := append(os.Environ(), "SSH_AUTH_SOCK="+sock)
	if runTool(ctx, s.toolPath("ssh-add"), []string{"-T", key + ".pub"}, base) == nil {
		return sock, nil
	}
	if s.AdminAuth.Passphrase == nil {
		return "", errAdminLocked(host)
	}
	pass, err := s.AdminAuth.Passphrase(host)
	if err != nil {
		return "", err
	}
	if err := s.loadAdmin(ctx, host, pass); err != nil {
		return "", err
	}
	return sock, nil
}

// loadAdmin adds host's admin key to the agent for the admin window.
func (s *Service) loadAdmin(ctx context.Context, host, passphrase string) error {
	sock, err := s.agentSock(ctx)
	if err != nil {
		return err
	}
	env, err := askpassEnviron(map[string]string{askpassSecret: passphrase})
	if err != nil {
		return err
	}
	env = append(env, "SSH_AUTH_SOCK="+sock)
	secs := strconv.Itoa(int(s.AdminWindow() / time.Second))
	if err := runTool(ctx, s.toolPath("ssh-add"), []string{"-q", "-t", secs, s.keyPath(host, adminKeyName)}, env); err != nil {
		return valise.New("admin_passphrase_wrong", "管理口令不对，没有解锁", "重新输入管理口令").WithDetail(err.Error())
	}
	s.agent.mu.Lock()
	if s.agent.unlocked == nil {
		s.agent.unlocked = map[string]time.Time{}
	}
	s.agent.unlocked[host] = time.Now()
	s.agent.mu.Unlock()
	return nil
}

// AdminUnlocked reports whether host's admin key is in the agent now.
func (s *Service) AdminUnlocked(ctx context.Context, host string) bool {
	s.agent.mu.Lock()
	sock := s.agent.sock
	s.agent.mu.Unlock()
	if sock == "" || !s.HasAdminKey(host) {
		return false
	}
	return runTool(ctx, s.toolPath("ssh-add"), []string{"-T", s.keyPath(host, adminKeyName) + ".pub"}, append(os.Environ(), "SSH_AUTH_SOCK="+sock)) == nil
}

// AdminRemaining is how much of host's admin window is left, as Valise
// recorded it when the key was unlocked; false when it is locked.
func (s *Service) AdminRemaining(host string) (time.Duration, bool) {
	s.agent.mu.Lock()
	t, ok := s.agent.unlocked[host]
	s.agent.mu.Unlock()
	if !ok {
		return 0, false
	}
	left := s.AdminWindow() - time.Since(t)
	return left, left > 0
}

// LockAdmin removes every unlocked admin key from the agent.
func (s *Service) LockAdmin() {
	s.agent.mu.Lock()
	sock := s.agent.sock
	s.agent.unlocked = nil
	s.agent.mu.Unlock()
	if sock != "" {
		_ = runTool(context.Background(), s.toolPath("ssh-add"), []string{"-D"}, append(os.Environ(), "SSH_AUTH_SOCK="+sock))
	}
}

// Close ends Valise's own ssh-agent.
func (s *Service) Close() {
	a := &s.agent
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cmd != nil && a.cmd.Process != nil {
		_ = a.cmd.Process.Kill()
	}
	a.cmd = nil
	if a.sock != "" {
		_ = os.Remove(a.sock)
		a.sock = ""
	}
}

// authorizedKeyLine is the authorized_keys line for a Valise key: the
// daily key is restricted to the porter's daily tier and loopback
// forwarding (docs/ARCHITECTURE.md 2.1). "port-forwarding" re-enables -R
// too; permitlisten has no "none", so it allows only privileged port 1,
// which the user's sshd process cannot bind. "pty" lets login relay run
// login-run in a terminal; the forced command still decides what runs.
func authorizedKeyLine(name, pub string) string {
	pub = strings.TrimSpace(pub)
	f := strings.Fields(pub)
	if len(f) >= 2 {
		pub = f[0] + " " + f[1] + " valise-" + name + "@" + localName()
	}
	if name == dailyKeyName {
		return `restrict,pty,port-forwarding,permitlisten="127.0.0.1:1",permitopen="127.0.0.1:*",permitopen="[::1]:*",command="` + dailyPorterPath + ` serve --tier daily" ` + pub
	}
	return pub
}

// installKeysScript appends the given authorized_keys lines, first dropping
// earlier lines with the same public keys (so re-installing is idempotent
// and other clients' Valise keys are kept). Sent on stdin to `sh -s`.
func installKeysScript(lines map[string]string) string {
	var drop, add strings.Builder
	for _, line := range lines {
		fmt.Fprintf(&drop, " -e '%s'", keyBlob(line))
		add.WriteString(line + "\n")
	}
	return `set -e
umask 077
mkdir -p "$HOME/.ssh"
chmod 700 "$HOME/.ssh"
f="$HOME/.ssh/authorized_keys"
touch "$f"
grep -v -F` + drop.String() + ` "$f" > "$f.valise-new" || true
cat >> "$f.valise-new" <<'VALISE_KEYS'
` + add.String() + `VALISE_KEYS
chmod 600 "$f.valise-new"
mv "$f.valise-new" "$f"
`
}

// keyBlob is the base64 key of an authorized_keys line (no shell or grep
// special characters: [A-Za-z0-9+/=]).
func keyBlob(line string) string {
	f := strings.Fields(line)
	for i, w := range f {
		if strings.HasPrefix(w, "ssh-") && i+1 < len(f) {
			return f[i+1]
		}
	}
	return ""
}

// localName names this computer in key comments, so the user can tell
// which client a line on the server belongs to.
func localName() string {
	h, _ := os.Hostname()
	h, _, _ = strings.Cut(h, ".")
	var b strings.Builder
	for _, r := range h {
		if r < 128 && (r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "client"
	}
	return b.String()
}

// keyLines reads the public keys to install.
func (s *Service) keyLines(host string, names ...string) (map[string]string, error) {
	out := map[string]string{}
	for _, n := range names {
		b, err := os.ReadFile(s.keyPath(host, n) + ".pub")
		if err != nil {
			return nil, err
		}
		out[n] = authorizedKeyLine(n, string(b))
	}
	return out, nil
}
