package service

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"valise/client/config"
	"valise/client/ssh"
	"valise/core/valise"
)

// Adding a server by hand (docs/ARCHITECTURE.md section 3): confirm the
// host key, log in once with the password (or the user's own key file),
// install the daily key (and the admin key if asked), then check, deploy,
// register and name over that same bootstrap login.

// HostKey is one host key offered by a server.
type HostKey struct {
	Type        string `json:"type"`
	Fingerprint string `json:"fingerprint"`
}

// Keyscan fetches a server's host keys in known_hosts format; tests replace
// it. The default runs ssh-keyscan.
type Keyscan func(ctx context.Context, host string, port int) ([]byte, error)

func (s *Service) keyscan(ctx context.Context, host string, port int) ([]byte, error) {
	if s.Keyscan != nil {
		return s.Keyscan(ctx, host, port)
	}
	if port == 0 {
		port = 22
	}
	out, err := exec.CommandContext(ctx, s.toolPath("ssh-keyscan"), "-T", "10", "-p", strconv.Itoa(port), "--", host).Output()
	if len(bytes.TrimSpace(out)) == 0 {
		return nil, valise.New("keyscan_failed", "连不上 "+host+"，没有拿到服务器的身份信息", "检查地址、端口和网络").WithDetail(fmt.Sprint(err))
	}
	return out, nil
}

// knownHostsField is how OpenSSH names a host in known_hosts.
func knownHostsField(host string, port int) string {
	if port == 0 || port == 22 {
		return host
	}
	return "[" + host + "]:" + strconv.Itoa(port)
}

// parseHostKeys keeps well-formed known_hosts lines for field and their
// SHA256 fingerprints.
func parseHostKeys(data []byte, field string) ([]HostKey, []string) {
	var keys []HostKey
	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 3 || strings.HasPrefix(f[0], "#") || f[0] != field {
			continue
		}
		blob, err := base64.StdEncoding.DecodeString(f[2])
		if err != nil {
			continue
		}
		sum := sha256.Sum256(blob)
		keys = append(keys, HostKey{Type: f[1], Fingerprint: "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])})
		lines = append(lines, f[0]+" "+f[1]+" "+f[2])
	}
	return keys, lines
}

// ScanHostKeys asks the server for its host keys, to show the fingerprints
// before anything is trusted. The scan is not authenticated: the user
// compares the fingerprints with the provider's console.
func (s *Service) ScanHostKeys(ctx context.Context, srv config.Server) ([]HostKey, error) {
	data, err := s.keyscan(ctx, srv.HostName, srv.Port)
	if err != nil {
		return nil, err
	}
	keys, _ := parseHostKeys(data, knownHostsField(srv.HostName, srv.Port))
	if len(keys) == 0 {
		return nil, valise.New("keyscan_failed", "服务器没有给出可用的身份信息", "确认地址与端口是 SSH 服务")
	}
	return keys, nil
}

// TrustHostKeys scans again and records the keys in Valise's known_hosts,
// refusing if they differ from the fingerprints the user confirmed.
func (s *Service) TrustHostKeys(ctx context.Context, srv config.Server, confirmed []HostKey) error {
	data, err := s.keyscan(ctx, srv.HostName, srv.Port)
	if err != nil {
		return err
	}
	field := knownHostsField(srv.HostName, srv.Port)
	keys, lines := parseHostKeys(data, field)
	// ssh-keyscan answers in no fixed order: compare as sets.
	sorted := func(k []HostKey) []HostKey {
		k = slices.Clone(k)
		slices.SortFunc(k, func(a, b HostKey) int { return strings.Compare(a.Type+a.Fingerprint, b.Type+b.Fingerprint) })
		return k
	}
	if !slices.Equal(sorted(keys), sorted(confirmed)) {
		return valise.New("host_key_changed_during_add", "服务器的身份在确认期间变了", "重新开始添加，并再次核对指纹")
	}
	return s.writeKnownHosts(field, lines)
}

func (s *Service) writeKnownHosts(field string, lines []string) error {
	path := filepath.Join(s.Dirs.Data, config.KnownHostsFile)
	old, _ := os.ReadFile(path)
	var keep []string
	for _, l := range strings.Split(string(old), "\n") {
		if f := strings.Fields(l); len(f) > 0 && f[0] != field {
			keep = append(keep, l)
		}
	}
	keep = append(keep, lines...)
	if err := os.MkdirAll(s.Dirs.Data, 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strings.Join(keep, "\n")+"\n"), 0o600)
}

// hostKeyKnown reports whether Valise's known_hosts has a key for srv.
func (s *Service) hostKeyKnown(ctx context.Context, srv config.Server) bool {
	path := filepath.Join(s.Dirs.Data, config.KnownHostsFile)
	if !fileExists(path) {
		return false
	}
	return exec.CommandContext(ctx, s.toolPath("ssh-keygen"), "-F", knownHostsField(srv.HostName, srv.Port), "-f", path).Run() == nil
}

// bootstrapTransport is the one full login of adding a server (or creating
// its admin key): password or the user's own key file, never Valise keys.
func (s *Service) bootstrapTransport(srv config.Server) (*ssh.Transport, error) {
	o := s.valiseOptions(srv)
	o.IdentityAgent = srv.LoginAgentOption()
	if srv.Login == config.LoginKey {
		o.Identity = srv.LoginKey
	} else {
		o.NoPubkey = true
	}
	if o.ControlPath = ssh.SocketPath(s.Dirs.Cache, "op-"); o.ControlPath == "" {
		o.NoControl = true
	}
	if s.canPrompt() {
		env, err := s.relayEnv()
		if err != nil {
			return nil, err
		}
		o.Env = env
	}
	return ssh.New(srv.Name, o)
}

// refuseNoAuth stops when the server (or a gateway in front of it) lets
// anyone in without a key or password: the daily key's restrictions live
// in authorized_keys, which such a server never consults, so a "daily" key
// there would be a full login (seen on a cloud VM behind an SSH gateway).
func (s *Service) refuseNoAuth(ctx context.Context, srv config.Server) error {
	// srv may not be in servers.json yet: probe with a config of its own.
	if err := os.MkdirAll(s.Dirs.Cache, 0o700); err != nil {
		return err
	}
	cfg, err := os.CreateTemp(s.Dirs.Cache, "probe-ssh_config-*")
	if err != nil {
		return err
	}
	defer os.Remove(cfg.Name())
	_, err = cfg.WriteString(config.RenderSSHConfig(s.Dirs.Data, []config.Server{srv}))
	if cerr := cfg.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	o := ssh.Options{Binary: s.Config.SSHBinary, ConfigFile: cfg.Name(), IdentityAgent: "none", NoPubkey: true, AuthNone: true, NoControl: true}
	t, err := ssh.New(srv.Name, o)
	if err != nil {
		return err
	}
	if res, err := t.Run(ctx, "true", nil); err == nil && res.ExitCode == 0 {
		return errNoRestriction(srv.Name, "这台服务器（或它前面的 SSH 网关）不需要任何钥匙或密码就能登录：知道地址和用户名的人都能进入")
	}
	return nil
}

func errNoRestriction(host, why string) *valise.UserError {
	return valise.New("daily_key_unenforceable", why+"。Valise 的日常钥匙在这里无法限制权限，所以不能用日常钥匙管理 "+host,
		"继续用你自己的 SSH 设置接入（valise server import），并留意这台服务器本身的登录风险")
}

// verifyRestricted runs an ordinary shell command with the daily key; it
// must be refused by the porter's forced command.
func (s *Service) verifyRestricted(ctx context.Context, host string) error {
	t, _, err := s.transport(host)
	if err != nil {
		return err
	}
	res, err := t.Run(ctx, "echo valise-unrestricted-probe", nil)
	if err == nil && res.ExitCode == 0 && strings.Contains(string(res.Stdout), "valise-unrestricted-probe") {
		return errNoRestriction(host, "服务器没有执行日常钥匙在 authorized_keys 里的限制，它拿到了完整的 shell")
	}
	return nil
}

// uninstallKeys removes this client's Valise keys for host from the
// server's authorized_keys (matched by public key).
func (s *Service) uninstallKeys(ctx context.Context, t *ssh.Transport, host string, names ...string) error {
	lines, err := s.keyLines(host, names...)
	if err != nil {
		return err
	}
	var drop strings.Builder
	for _, l := range lines {
		fmt.Fprintf(&drop, " -e '%s'", keyBlob(l))
	}
	res, err := t.Run(ctx, "sh -s", strings.NewReader(`set -e
f="$HOME/.ssh/authorized_keys"
[ -f "$f" ] || exit 0
grep -v -F`+drop.String()+` "$f" > "$f.valise-new" || true
chmod 600 "$f.valise-new"
mv "$f.valise-new" "$f"
`))
	if err == nil && res.ExitCode != 0 {
		err = fmt.Errorf("exit %d: %s", res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
	return err
}

func (s *Service) installKeys(ctx context.Context, t *ssh.Transport, host string, names ...string) error {
	lines, err := s.keyLines(host, names...)
	if err != nil {
		return err
	}
	res, err := t.Run(ctx, "sh -s", strings.NewReader(installKeysScript(lines)))
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return valise.New("install_keys_failed", "没能把钥匙写进服务器的 ~/.ssh/authorized_keys", "检查服务器上 ~/.ssh 的权限与磁盘空间").
			WithDetail(strings.TrimSpace(string(res.Stderr)))
	}
	if slices.Contains(names, dailyKeyName) {
		s.markDailyTTY(host)
	}
	return nil
}

// markDailyTTY records that host's daily key line now allows a terminal.
func (s *Service) markDailyTTY(host string) {
	list, err := config.LoadServerList(s.Dirs.Data)
	if err != nil {
		return
	}
	if i := slices.IndexFunc(list, func(x config.Server) bool { return x.Name == host }); i >= 0 && !list[i].DailyTTY {
		list[i].DailyTTY = true
		_ = config.SaveServerList(s.Dirs.Data, list)
	}
}

// Steps of adding a server by hand, in addition to the setup steps.
const (
	SetupKeys  = "keys"
	SetupDaily = "daily"
)

// AddServerOptions configure adding a server by hand.
type AddServerOptions struct {
	Server config.Server
	// AdminPassphrase creates the admin key when supplied. Frontends with a
	// Valise passphrase verify it before passing it here.
	AdminPassphrase string
	Setup           SetupOptions
}

// AddServer records a server added by hand and sets it up. The host key
// must have been confirmed with TrustHostKeys first.
func (s *Service) AddServer(ctx context.Context, o AddServerOptions) error {
	stopOp := s.BeginOperation()
	defer stopOp()
	srv := o.Server
	srv.Source = config.SourceValise
	if srv.Login == "" {
		srv.Login = config.LoginPassword
	}
	if err := srv.Validate(); err != nil {
		return valise.New("server_invalid", err.Error(), "修改后重试")
	}
	list, err := config.LoadServerList(s.Dirs.Data)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(list, func(x config.Server) bool { return x.Name == srv.Name })
	if i >= 0 && !list[i].Valise() {
		return valise.New("server_exists", "已经有一台叫 "+srv.Name+" 的服务器（来自 SSH 配置）", "换一个别名")
	}
	if !s.hostKeyKnown(ctx, srv) {
		return valise.New("host_key_unconfirmed", "还没有确认这台服务器的身份", "先核对并确认服务器的指纹")
	}
	if err := s.refuseNoAuth(ctx, srv); err != nil {
		return err
	}
	if i >= 0 {
		list[i] = srv
	} else {
		list = append(list, srv)
	}
	if err := config.SaveServerList(s.Dirs.Data, list); err != nil {
		return err
	}
	s.unmarkRemoved(srv.Name)
	o.Setup.doing(SetupKeys, "生成用户通行证")
	keys := []string{dailyKeyName}
	if err := s.ensureDailyKey(ctx, srv.Name); err != nil {
		return err
	}
	if o.AdminPassphrase != "" {
		o.Setup.doing(SetupKeys, "生成并加密管理员通行证")
		if err := s.createAdminKey(ctx, srv.Name, o.AdminPassphrase); err != nil {
			return err
		}
		keys = append(keys, adminKeyName)
	}
	t, err := s.bootstrapTransport(srv)
	if err != nil {
		return err
	}
	defer t.CloseMaster()
	o.Setup.doing(SetupKeys, loginDoing(srv))
	if err := s.installKeys(ctx, t, srv.Name, keys...); err != nil {
		return err
	}
	step := o.Setup.step
	step(SetupStep{Step: SetupKeys, OK: true, Detail: strings.Join(keys, "、")})
	hc := ssh.HostConfig{Alias: srv.Name, HostName: srv.HostName, User: srv.User, Port: srv.Port}
	if err := s.setupOn(ctx, t, hc, o.Setup); err != nil {
		return err
	}
	o.Setup.doing(SetupDaily, "核对用户通行证只能查看和备份")
	if err := s.verifyRestricted(ctx, srv.Name); err != nil {
		_ = s.uninstallKeys(ctx, t, srv.Name, keys...)
		s.forgetServer(srv.Name)
		return err
	}
	if o.AdminPassphrase != "" {
		_ = s.loadAdmin(ctx, srv.Name, o.AdminPassphrase)
	}
	o.Setup.doing(SetupDaily, "用用户通行证连接 porter")
	return s.checkDaily(ctx, srv.Name, step)
}

// loginDoing says what the one login is waiting for.
func loginDoing(srv config.Server) string {
	addr := knownHostsField(srv.HostName, srv.Port)
	if srv.Login == config.LoginKey {
		return "用你的私钥登录 " + addr + "，写入通行证（SSH Agent 要你确认时，请在它的窗口里放行）"
	}
	return "用 SSH 连接 " + addr + "，登录后写入通行证（OpenSSH 会问服务器密码）"
}

// forgetServer drops a server added by hand from the list.
func (s *Service) forgetServer(host string) {
	list, err := config.LoadServerList(s.Dirs.Data)
	if err != nil {
		return
	}
	if i := slices.IndexFunc(list, func(x config.Server) bool { return x.Name == host }); i >= 0 {
		_ = config.SaveServerList(s.Dirs.Data, slices.Delete(list, i, i+1))
	}
}

// checkDaily confirms the daily key now reaches the porter.
func (s *Service) checkDaily(ctx context.Context, host string, step func(SetupStep)) error {
	doc, err := s.Doctor(ctx, host)
	if err != nil {
		return err
	}
	if doc.Problem != nil {
		return valise.New("daily_key_unusable", "日常钥匙已安装，但还连不上服务器上的 porter："+doc.Problem.Reason, doc.Problem.Suggestion)
	}
	step(SetupStep{Step: SetupDaily, OK: true, Detail: "porter " + doc.PorterVersion})
	return nil
}

// CreateAdminKey creates host's admin key and installs it with one
// temporary login (password or the user's own key file).
func (s *Service) CreateAdminKey(ctx context.Context, host, passphrase string) error {
	stopOp := s.BeginOperation()
	defer stopOp()
	srv := s.server(host)
	if !srv.Valise() {
		return valise.New("admin_key_untiered", host+" 用的是你自己的 SSH 设置，不需要管理钥匙", "先迁移到 Valise 日常钥匙")
	}
	if s.HasAdminKey(host) {
		return valise.New("admin_key_exists", host+" 已经有管理钥匙", "要换口令请用修改管理口令")
	}
	if err := s.createAdminKey(ctx, host, passphrase); err != nil {
		return err
	}
	t, err := s.bootstrapTransport(srv)
	if err != nil {
		return err
	}
	defer t.CloseMaster()
	if err := s.installKeys(ctx, t, host, adminKeyName); err != nil {
		return err
	}
	return s.loadAdmin(ctx, host, passphrase)
}

// RemoveAdminKey takes host's admin key off the server (over a developer
// mode login, which may be the key itself) and deletes it here; admin
// operations then ask for the server password each time.
func (s *Service) RemoveAdminKey(ctx context.Context, host string) error {
	if !s.HasAdminKey(host) {
		return valise.New("admin_key_none", host+" 没有管理钥匙", "")
	}
	t, done, err := s.adminTransport(ctx, host)
	if err != nil {
		return err
	}
	err = s.uninstallKeys(ctx, t, host, adminKeyName)
	done()
	if err != nil {
		return valise.New("admin_key_remove_failed", "没能从服务器上移除管理钥匙", "稍后重试；这台电脑上的钥匙还在").WithDetail(err.Error())
	}
	s.LockAdmin()
	p := s.keyPath(host, adminKeyName)
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Remove(p + ".pub"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// MigrateServer moves a server from ~/.ssh/config to Valise's own keys: the
// daily key (and the admin key if a passphrase is given) is installed over
// the user's existing login, and the host key already trusted in
// ~/.ssh/known_hosts is copied. Hosts behind ProxyJump are refused.
func (s *Service) MigrateServer(ctx context.Context, host, adminPassphrase string) (porterMissing bool, err error) {
	stopOp := s.BeginOperation()
	defer stopOp()
	srv := s.server(host)
	if srv.Valise() {
		return false, valise.New("already_migrated", host+" 已经在用 Valise 日常钥匙", "")
	}
	c, err := s.SSHConfig()
	if err != nil {
		return false, err
	}
	hc := c.Lookup(host)
	if hc.ProxyJump != "" {
		return false, valise.New("migrate_proxyjump", host+" 经跳板机连接，暂时不能迁移", "继续使用你自己的 SSH 设置")
	}
	n := config.Server{Name: host, Source: config.SourceValise, HostName: hc.HostName, Port: hc.Port, User: hc.User, Login: config.LoginPassword}
	if n.HostName == "" {
		n.HostName = host
	}
	for _, f := range hc.IdentityFiles {
		if filepath.IsAbs(f) && fileExists(f) {
			n.Login, n.LoginKey = config.LoginKey, f
			// A public key names a key held by the user's agent (as
			// password managers set it up): keep using that agent.
			if strings.HasSuffix(f, ".pub") {
				n.LoginAgent = loginAgent(hc.IdentityAgent)
			}
			break
		}
	}
	if err := n.Validate(); err != nil {
		return false, valise.New("server_invalid", err.Error(), "检查 ~/.ssh/config 中的设置")
	}
	field := knownHostsField(n.HostName, n.Port)
	out, _ := exec.CommandContext(ctx, s.toolPath("ssh-keygen"), "-F", field, "-f", filepath.Join(s.Home, ".ssh", "known_hosts")).Output()
	var lines []string
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 {
		return false, valise.New("migrate_no_host_key", "在 ~/.ssh/known_hosts 里找不到 "+field+" 的身份记录", "先用 ssh 登录一次这台服务器")
	}
	if err := s.writeKnownHosts(field, lines); err != nil {
		return false, err
	}
	if err := s.refuseNoAuth(ctx, n); err != nil {
		return false, err
	}
	keys := []string{dailyKeyName}
	if err := s.ensureDailyKey(ctx, host); err != nil {
		return false, err
	}
	if adminPassphrase != "" {
		if err := s.createAdminKey(ctx, host, adminPassphrase); err != nil {
			return false, err
		}
		keys = append(keys, adminKeyName)
	}
	t, done, err := s.adminTransport(ctx, host)
	if err != nil {
		return false, err
	}
	defer done()
	if err := s.installKeys(ctx, t, host, keys...); err != nil {
		return false, err
	}
	old, err := config.LoadServerList(s.Dirs.Data)
	if err != nil {
		return false, err
	}
	list := slices.Clone(old)
	n.DailyTTY = true
	if i := slices.IndexFunc(list, func(x config.Server) bool { return x.Name == host }); i >= 0 {
		list[i] = n
	} else {
		list = append(list, n)
	}
	if err := config.SaveServerList(s.Dirs.Data, list); err != nil {
		return false, err
	}
	if err := s.verifyRestricted(ctx, host); err != nil {
		_ = s.uninstallKeys(ctx, t, host, keys...)
		_ = config.SaveServerList(s.Dirs.Data, old)
		return false, err
	}
	if adminPassphrase != "" {
		_ = s.loadAdmin(ctx, host, adminPassphrase)
	}
	// Not deployed yet is the expected next step, not a failure.
	if doc, err := s.Doctor(ctx, host); err == nil && doc.Problem != nil && doc.Problem.Code == "porter_not_found" {
		return true, nil
	}
	return false, s.checkDaily(ctx, host, func(SetupStep) {})
}

// loginAgent is the agent a migrated key login keeps using: the one the
// user's ~/.ssh/config names, or the environment's.
func loginAgent(configured string) string {
	if configured == "" || strings.EqualFold(configured, "none") {
		return "SSH_AUTH_SOCK"
	}
	return strings.Trim(configured, `"`)
}
