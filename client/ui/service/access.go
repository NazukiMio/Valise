package service

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"

	"valise/client/config"
	"valise/client/porter"
	"valise/client/ssh"
	"valise/core/rpc"
	"valise/core/valise"
)

// Access levels (docs/ARCHITECTURE.md 2.1). Servers added by hand use the
// daily key for daily operations and an admin login for the rest; servers
// from ~/.ssh/config use the user's own key for everything.

// dailyPorterPath is the daily key's forced-command target. The daily
// porter parses the command line itself, so "$HOME" is never expanded by a
// shell on that path.
const dailyPorterPath = "$HOME/.local/bin/valise-porter"

// Key file names inside config.KeyDir.
const (
	dailyKeyName = "daily"
	adminKeyName = "admin"
)

// AdminAuth lets a frontend supply secrets for admin operations. Secrets
// are used once and never stored.
type AdminAuth struct {
	// Passphrase asks for the admin passphrase to unlock host's admin key.
	Passphrase func(host string) (string, error)
}

// ErrAdminLocked is returned when an admin key must be unlocked and no
// frontend can ask for the passphrase.
func errAdminLocked(host string) *valise.UserError {
	return valise.New("admin_locked", "这一步需要管理权限，管理钥匙还没有解锁（"+host+"）",
		"在界面里输入管理口令，或在终端运行同一条命令时按提示输入")
}

// server returns host's record; unknown hosts read as SourceSSHConfig.
func (s *Service) server(host string) config.Server {
	srv, ok, err := config.FindServer(s.Dirs.Data, host)
	if err != nil || !ok {
		return config.Server{Name: host, Source: config.SourceSSHConfig}
	}
	return srv
}

func (s *Service) keyPath(host, name string) string {
	return filepath.Join(config.KeyDir(s.Dirs.Data, host), name)
}

// HasAdminKey reports whether an admin key was created for host.
func (s *Service) HasAdminKey(host string) bool {
	_, err := os.Stat(s.keyPath(host, adminKeyName))
	return err == nil
}

// Tiered reports whether host separates daily and admin access (a server
// added by hand with Valise's daily key).
func (s *Service) Tiered(host string) bool { return s.server(host).Valise() }

func (s *Service) valiseOptions(srv config.Server) ssh.Options {
	return ssh.Options{Binary: s.Config.SSHBinary, ConfigFile: filepath.Join(s.Dirs.Data, config.SSHConfigFile)}
}

// transport is the daily transport: the daily key on servers added by
// hand (never prompting), the user's own SSH setup otherwise.
func (s *Service) transport(host string) (*ssh.Transport, ssh.HostConfig, error) {
	srv := s.server(host)
	if !srv.Valise() {
		return s.userTransport(host)
	}
	o := s.valiseOptions(srv)
	o.Identity, o.IdentityAgent, o.BatchMode, o.ControlDir = s.keyPath(host, dailyKeyName), "none", true, s.Dirs.Cache
	o.ControlName = "cmd-" // never shared with a full login's master
	t, err := ssh.New(host, o)
	if err != nil {
		return nil, ssh.HostConfig{}, valise.New("bad_host", "服务器别名无效："+host, "检查服务器列表")
	}
	return t, ssh.HostConfig{Alias: host, HostName: srv.HostName, User: srv.User, Port: srv.Port}, nil
}

func (s *Service) userTransport(host string) (*ssh.Transport, ssh.HostConfig, error) {
	c, err := s.SSHConfig()
	if err != nil {
		return nil, ssh.HostConfig{}, err
	}
	hc := c.Lookup(host)
	t, err := ssh.New(host, ssh.Options{Binary: s.Config.SSHBinary, ControlDir: s.Dirs.Cache, HostConfig: &hc})
	if err != nil {
		return nil, hc, valise.New("bad_host", "服务器别名无效："+host, "使用 ~/.ssh/config 中的 Host 别名，或 user@host")
	}
	return t, hc, nil
}

// adminTransport opens the transport for one admin operation: its own
// master connection, never the daily one. On servers added by hand it uses
// the unlocked admin key, or a temporary login with the server password
// (OpenSSH asks on the terminal) or the user's own key file. done ends the
// operation's master connection.
func (s *Service) adminTransport(ctx context.Context, host string) (t *ssh.Transport, done func(), err error) {
	srv := s.server(host)
	var o ssh.Options
	if srv.Valise() {
		o = s.valiseOptions(srv)
		switch {
		case s.HasAdminKey(host):
			sock, err := s.unlockAdmin(ctx, host)
			if err != nil {
				return nil, nil, err
			}
			o.Identity, o.IdentityAgent = s.keyPath(host, adminKeyName), sock
		case srv.Login == config.LoginKey:
			o.Identity, o.IdentityAgent, o.BatchMode = srv.LoginKey, "none", s.NoTerminalLogin
		case s.NoTerminalLogin:
			return nil, nil, valise.New("admin_needs_password", "这一步需要服务器密码，终端界面里还不能输入它（"+host+"）",
				"创建管理钥匙（valise server admin-key "+host+"）后在这里输入管理口令即可；或者在命令行里执行这一步")
		default:
			o.NoPubkey, o.IdentityAgent = true, "none"
		}
	} else {
		c, err := s.SSHConfig()
		if err != nil {
			return nil, nil, err
		}
		hc := c.Lookup(host)
		o = ssh.Options{Binary: s.Config.SSHBinary, HostConfig: &hc}
	}
	if o.ControlPath = ssh.SocketPath(s.Dirs.Cache, "op-"); o.ControlPath == "" {
		o.NoControl = true
	}
	if t, err = ssh.New(host, o); err != nil {
		return nil, nil, valise.New("bad_host", "服务器别名无效："+host, "检查服务器列表")
	}
	return t, t.CloseMaster, nil
}

// porterOn locates the porter reachable through t: the fixed path on the
// daily tier, the probe chain on a full login.
func (s *Service) porterOn(ctx context.Context, t *ssh.Transport, daily bool) (*porter.Found, error) {
	if !daily {
		return porter.Probe(ctx, t)
	}
	f, err := porter.VersionAt(ctx, t, dailyPorterPath, ssh.Quote(dailyPorterPath))
	if ue, ok := valise.As(err); ok && ue.Code == "porter_unusable" {
		return nil, porter.ErrNotFound(t.Host())
	}
	return f, err
}

// porterAbsPath is the porter's absolute path on host, for commands that
// run there outside Valise (the MCP server line given to the harness).
func (s *Service) porterAbsPath(ctx context.Context, host string) (string, error) {
	t, _, err := s.transport(host)
	if err != nil {
		return "", err
	}
	if !s.Tiered(host) {
		f, err := porter.Probe(ctx, t)
		if err != nil {
			return "", err
		}
		return f.Path, nil
	}
	c, hello, err := ssh.Dial(ctx, t, dailyPorterPath, s.Version)
	if err != nil {
		return "", err
	}
	c.Close()
	return path.Join(hello.Home, ".local", "bin", "valise-porter"), nil
}

// caller is one porter RPC endpoint.
type caller interface {
	Call(ctx context.Context, method string, params, result any) error
}

// porterSession routes each RPC by tier: daily methods on the daily
// connection, admin methods on an admin connection opened on first use.
// Untiered servers use a single connection.
type porterSession struct {
	s      *Service
	host   string
	tiered bool
	daily  *ssh.RPCClient
	admin  *ssh.RPCClient
	done   func()
}

// session dials the porter on host at the daily tier.
func (s *Service) session(ctx context.Context, host string) (*porterSession, error) {
	t, _, err := s.transport(host)
	if err != nil {
		return nil, err
	}
	tiered := s.Tiered(host)
	found, err := s.porterOn(ctx, t, tiered)
	if err != nil {
		return nil, err
	}
	c, _, err := ssh.Dial(ctx, t, found.Path, s.Version)
	if err != nil {
		return nil, err
	}
	return &porterSession{s: s, host: host, tiered: tiered, daily: c}, nil
}

// withSession runs fn on a porter session for host.
func (s *Service) withSession(ctx context.Context, host string, fn func(caller) error) error {
	c, err := s.session(ctx, host)
	if err != nil {
		return err
	}
	defer c.Close()
	return fn(c)
}

func (p *porterSession) Call(ctx context.Context, method string, params, result any) error {
	if !p.tiered {
		return p.daily.Call(ctx, method, params, result)
	}
	if rpc.TierOf(method) == rpc.TierDaily {
		err := p.daily.Call(ctx, method, params, result)
		if ue, ok := valise.As(err); !ok || ue.Code != rpc.AdminRequiredCode || !rpc.Conditional(method) {
			return err
		}
	}
	c, err := p.adminClient(ctx)
	if err != nil {
		return err
	}
	return c.Call(ctx, method, params, result)
}

func (p *porterSession) adminClient(ctx context.Context) (*ssh.RPCClient, error) {
	if p.admin != nil {
		return p.admin, nil
	}
	t, done, err := p.s.adminTransport(ctx, p.host)
	if err != nil {
		return nil, err
	}
	found, err := p.s.porterOn(ctx, t, false)
	if err != nil {
		done()
		return nil, err
	}
	c, _, err := ssh.Dial(ctx, t, found.Path, p.s.Version)
	if err != nil {
		done()
		return nil, err
	}
	p.admin, p.done = c, done
	return c, nil
}

func (p *porterSession) Close() error {
	err := p.daily.Close()
	if p.admin != nil {
		err = errors.Join(err, p.admin.Close())
		p.done()
	}
	return err
}
