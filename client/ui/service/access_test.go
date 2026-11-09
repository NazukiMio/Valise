package service

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"valise/client/config"
	"valise/client/ssh"
	"valise/core/install"
	"valise/core/rpc"
	"valise/core/vault"
	"valise/core/vaultrpc"
	"valise/testbed/fakesshenv"
)

// needOpenSSH skips when the local OpenSSH tools used for Valise's own
// keys are missing.
func needOpenSSH(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"ssh-keygen", "ssh-agent", "ssh-add"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not installed")
		}
	}
}

// fakeHostKey returns a keyscan stub serving one real ed25519 host key.
func fakeHostKey(t *testing.T) Keyscan {
	dir := t.TempDir()
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", filepath.Join(dir, "hk")).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	pub, _ := os.ReadFile(filepath.Join(dir, "hk.pub"))
	f := strings.Fields(string(pub))
	return func(ctx context.Context, host string, port int) ([]byte, error) {
		return []byte("# comment\n" + knownHostsField(host, port) + " " + f[0] + " " + f[1] + "\n"), nil
	}
}

func addVPS(t *testing.T, adminPass string) (*Service, *fakesshenv.Env, string) {
	t.Helper()
	needOpenSSH(t)
	e := fakesshenv.New(t)
	home := e.AddHost("vps")
	s := newService(t, e.SSH)
	s.Keyscan = fakeHostKey(t)
	t.Cleanup(s.Close)
	ctx := context.Background()
	srv := config.Server{Name: "vps", HostName: "203.0.113.9", Port: 2222, User: "me"}

	err := s.AddServer(ctx, AddServerOptions{Server: srv, Setup: SetupOptions{Deploy: DeploySource{LocalPath: e.Porter()}}})
	wantCode(t, err, "host_key_unconfirmed")
	keys, err := s.ScanHostKeys(ctx, srv)
	if err != nil || len(keys) != 1 || !strings.HasPrefix(keys[0].Fingerprint, "SHA256:") {
		t.Fatalf("%+v %v", keys, err)
	}
	if err := s.TrustHostKeys(ctx, srv, keys); err != nil {
		t.Fatal(err)
	}
	var steps, doing []string
	err = s.AddServer(ctx, AddServerOptions{Server: srv, AdminPassphrase: adminPass,
		Setup: SetupOptions{Deploy: DeploySource{LocalPath: e.Porter()}, Step: func(st SetupStep) { steps = append(steps, st.Step) },
			Doing: func(step, what string) { doing = append(doing, step+":"+what) }}})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(steps, ","); got != "keys,check,deploy,register,name,daily" {
		t.Fatalf("steps %s", got)
	}
	// Each step says what it is doing before it may take a while.
	all := strings.Join(doing, "\n")
	for _, want := range []string{"keys:生成用户通行证", "keys:用 SSH 连接", "check:连接服务器", "register:连接 porter", "daily:用用户通行证连接 porter"} {
		if !strings.Contains(all, want) {
			t.Errorf("no %q in %s", want, all)
		}
	}
	return s, e, home
}

func TestAddServerByHand(t *testing.T) {
	s, _, home := addVPS(t, "")
	ctx := context.Background()

	ak, _ := os.ReadFile(filepath.Join(home, ".ssh", "authorized_keys"))
	if !strings.Contains(string(ak), `command="$HOME/.local/bin/valise-porter serve --tier daily" ssh-ed25519 `) ||
		!strings.Contains(string(ak), `permitopen="127.0.0.1:*"`) || !strings.Contains(string(ak), `permitlisten="127.0.0.1:1"`) || strings.Contains(string(ak), "valise-admin") {
		t.Fatalf("authorized_keys:\n%s", ak)
	}
	cfg, _ := os.ReadFile(filepath.Join(s.Dirs.Data, config.SSHConfigFile))
	if !strings.Contains(string(cfg), "Host vps\n  HostName 203.0.113.9\n  Port 2222\n  User me\n") {
		t.Fatalf("ssh_config:\n%s", cfg)
	}
	if fi, err := os.Stat(s.keyPath("vps", dailyKeyName)); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("daily key: %v %v", fi, err)
	}
	if !s.Tiered("vps") || s.HasAdminKey("vps") {
		t.Fatal("tiers")
	}

	// Daily operations go over the daily key's forced command.
	if _, err := s.Instances(ctx, "vps"); err != nil {
		t.Fatal(err)
	}
	d, err := s.Doctor(ctx, "vps")
	if err != nil || d.Problem != nil || d.Home != home {
		t.Fatalf("%+v %v", d, err)
	}

	// The daily key itself refuses admin methods and shell commands.
	tr, _, err := s.transport("vps")
	if err != nil {
		t.Fatal(err)
	}
	c, _, err := ssh.Dial(ctx, tr, dailyPorterPath, s.Version)
	if err != nil {
		t.Fatal(err)
	}
	err = c.Call(ctx, vaultrpc.MethodPeriodSet, vaultrpc.PeriodSetParams{InstanceID: "x", Setting: vault.PeriodSetting{Unit: vault.UnitWeek, TZ: "UTC"}}, nil)
	wantCode(t, err, rpc.OwnerRequiredCode)
	err = c.Call(ctx, install.MethodCheck, install.CheckParams{}, nil)
	c.Close()
	wantCode(t, err, rpc.AdminRequiredCode)
	res, err := tr.Run(ctx, "id", nil)
	if err != nil || res.ExitCode != 126 {
		t.Fatalf("shell on the daily key: %+v %v", res, err)
	}

	// An admin method opens its own temporary login (no admin key: the
	// password, which the fake host accepts).
	if err := s.call(ctx, "vps", install.MethodCheck, install.CheckParams{}, nil); err != nil {
		t.Fatal(err)
	}
	// An owner method stays on the daily key once the Valise passphrase
	// opens porter's challenge (docs/ARCHITECTURE.md 2.2).
	if _, err := s.KeyInit(pass); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.KeyRegister(ctx, "vps", "laptop"); err != nil {
		t.Fatal(err)
	}
	wantCode(t, s.PeriodSet(ctx, "vps", "x", vault.PeriodSetting{Unit: vault.UnitWeek, TZ: "UTC"}), "owner_locked")
	s.OwnerAuth.Passphrase = func(string) (string, error) { return "wrong passphrase!!", nil }
	if err := s.PeriodSet(ctx, "vps", "x", vault.PeriodSetting{Unit: vault.UnitWeek, TZ: "UTC"}); err == nil {
		t.Fatal("wrong Valise passphrase accepted")
	}
	asked := 0
	s.OwnerAuth.Passphrase = func(string) (string, error) { asked++; return pass, nil }
	for range 2 {
		if err := s.PeriodSet(ctx, "vps", "x", vault.PeriodSetting{Unit: vault.UnitWeek, TZ: "UTC"}); err != nil {
			t.Fatal(err)
		}
	}
	if asked != 1 {
		t.Fatalf("asked %d times", asked)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "valise", "instances", "x.vault.json")); err != nil {
		t.Fatal(err)
	}
}

func TestAdminKey(t *testing.T) {
	const adminPass = "admin passphrase 123"
	s, _, home := addVPS(t, adminPass)
	ctx := context.Background()
	ak, _ := os.ReadFile(filepath.Join(home, ".ssh", "authorized_keys"))
	if strings.Count(string(ak), "valise-daily@") != 1 || strings.Count(string(ak), " valise-admin@") != 1 {
		t.Fatalf("authorized_keys:\n%s", ak)
	}
	if !s.HasAdminKey("vps") || !s.AdminUnlocked(ctx, "vps") {
		t.Fatal("admin key should be created and unlocked")
	}
	admin := func() error { return s.call(ctx, "vps", install.MethodCheck, install.CheckParams{}, nil) }
	if err := admin(); err != nil {
		t.Fatal(err)
	}

	// Locked, without a frontend to ask: refused.
	s.LockAdmin()
	if s.AdminUnlocked(ctx, "vps") {
		t.Fatal("still unlocked")
	}
	wantCode(t, admin(), "admin_locked")
	// A wrong passphrase does not unlock.
	s.AdminAuth.Passphrase = func(string) (string, error) { return "wrong passphrase!!", nil }
	wantCode(t, admin(), "admin_passphrase_wrong")
	asked := 0
	s.AdminAuth.Passphrase = func(string) (string, error) { asked++; return adminPass, nil }
	if err := admin(); err != nil {
		t.Fatal(err)
	}
	if err := admin(); err != nil || asked != 1 {
		t.Fatalf("asked %d times: %v", asked, err)
	}
	s.AdminAuth.Passphrase = func(string) (string, error) { return "", errors.New("cancelled") }

	if err := s.CreateAdminKey(ctx, "vps", adminPass); err == nil {
		t.Fatal("second admin key")
	}

	// Changing the admin passphrase re-encrypts the key.
	if _, err := s.ChangeAdminPassphrase(ctx, adminPass, "short"); err == nil {
		t.Fatal("short passphrase accepted")
	}
	if n, err := s.ChangeAdminPassphrase(ctx, adminPass, "new admin passphrase"); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	s.AdminAuth.Passphrase = func(string) (string, error) { return "new admin passphrase", nil }
	if err := admin(); err != nil {
		t.Fatal(err)
	}
}

func TestAdminPassphraseDiffersFromValise(t *testing.T) {
	needOpenSSH(t)
	s := newService(t, "ssh")
	if _, err := s.KeyInit(pass); err != nil {
		t.Fatal(err)
	}
	wantCode(t, s.checkAdminPassphrase(pass), "admin_passphrase_same")
	wantCode(t, s.checkAdminPassphrase("short"), "admin_passphrase_short")
	if err := s.checkAdminPassphrase("another long passphrase"); err != nil {
		t.Fatal(err)
	}
}

func TestFirstRecipientOnDailyKey(t *testing.T) {
	s, _, home := addVPS(t, "")
	ctx := context.Background()
	recipients := filepath.Join(home, ".config", "valise", "recipients.txt")
	if _, err := os.Stat(recipients); err == nil {
		t.Fatal("recipient registered without a key")
	}
	if _, err := s.KeyInit(pass); err != nil {
		t.Fatal(err)
	}
	// Make any admin login fail: a temporary login now offers a key file
	// the fake host does not know.
	list, _ := config.LoadServerList(s.Dirs.Data)
	list[0].Login, list[0].LoginKey = config.LoginKey, s.keyPath("vps", "nonexistent")
	if err := config.SaveServerList(s.Dirs.Data, list); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.KeyRegister(ctx, "vps", "laptop"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(recipients); !strings.Contains(string(b), "age1") {
		t.Fatalf("recipients: %s", b)
	}
	// A second registration needs the owner: the daily key plus the
	// Valise passphrase, no admin login.
	_, _, err := s.KeyRegister(ctx, "vps", "laptop")
	wantCode(t, err, "owner_locked")
	s.OwnerAuth.Passphrase = func(string) (string, error) { return pass, nil }
	if _, _, err := s.KeyRegister(ctx, "vps", "laptop"); err != nil {
		t.Fatal(err)
	}
}

func TestInstallKeysKeepsOtherClients(t *testing.T) {
	s, _, home := addVPS(t, "")
	ctx := context.Background()
	ak := filepath.Join(home, ".ssh", "authorized_keys")
	other := `restrict,command="x" ssh-ed25519 AAAAotherclient valise-daily@desktop` + "\n"
	b, _ := os.ReadFile(ak)
	os.WriteFile(ak, append(b, other...), 0o600)
	// Re-installing this client's daily key replaces only its own line.
	tr, err := s.bootstrapTransport(s.server("vps"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.installKeys(ctx, tr, "vps", dailyKeyName); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(ak)
	if !strings.Contains(string(got), "AAAAotherclient") || strings.Count(string(got), "valise-daily@") != 2 {
		t.Fatalf("authorized_keys:\n%s", got)
	}
}

// A server that lets anyone in (InstaBox's SSH gateway authenticates with
// "none") cannot restrict a daily key: adding it by hand is refused before
// any key is installed.
func TestAddServerRefusesNoAuthGateway(t *testing.T) {
	needOpenSSH(t)
	e := fakesshenv.New(t)
	home := e.AddHost("box")
	t.Setenv("FAKESSH_AUTH_NONE", "box")
	s := newService(t, e.SSH)
	s.Keyscan = fakeHostKey(t)
	t.Cleanup(s.Close)
	ctx := context.Background()
	srv := config.Server{Name: "box", HostName: "198.51.100.4"}
	keys, _ := s.ScanHostKeys(ctx, srv)
	if err := s.TrustHostKeys(ctx, srv, keys); err != nil {
		t.Fatal(err)
	}
	err := s.AddServer(ctx, AddServerOptions{Server: srv, Setup: SetupOptions{Deploy: DeploySource{LocalPath: e.Porter()}}})
	wantCode(t, err, "daily_key_unenforceable")
	if _, err := os.Stat(filepath.Join(home, ".ssh", "authorized_keys")); err == nil {
		t.Fatal("keys installed on a server that lets anyone in")
	}
	if list, _ := s.ServerList(); len(list) != 0 {
		t.Fatalf("recorded: %+v", list)
	}
}

// A server that ignores authorized_keys options would give the daily key a
// full shell: the keys are removed again and the server is not recorded.
func TestAddServerRefusesIgnoredRestrictions(t *testing.T) {
	needOpenSSH(t)
	e := fakesshenv.New(t)
	home := e.AddHost("box")
	t.Setenv("FAKESSH_IGNORE_KEYS", "box")
	s := newService(t, e.SSH)
	s.Keyscan = fakeHostKey(t)
	t.Cleanup(s.Close)
	ctx := context.Background()
	srv := config.Server{Name: "box", HostName: "198.51.100.4"}
	keys, _ := s.ScanHostKeys(ctx, srv)
	if err := s.TrustHostKeys(ctx, srv, keys); err != nil {
		t.Fatal(err)
	}
	err := s.AddServer(ctx, AddServerOptions{Server: srv, Setup: SetupOptions{Deploy: DeploySource{LocalPath: e.Porter()}}})
	wantCode(t, err, "daily_key_unenforceable")
	if b, _ := os.ReadFile(filepath.Join(home, ".ssh", "authorized_keys")); strings.Contains(string(b), "valise-daily") {
		t.Fatalf("daily key left behind:\n%s", b)
	}
	if list, _ := s.ServerList(); len(list) != 0 {
		t.Fatalf("recorded: %+v", list)
	}
}
