package main

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"valise/client/config"
	"valise/core/valise"
	"valise/ui/service"
)

// Servers added by hand, the daily key and admin logins
// (docs/ARCHITECTURE.md sections 2.1 and 3).

var setupLabels = map[string]string{
	service.SetupKeys: "安装钥匙", service.SetupCheck: "检查服务器", service.SetupDeploy: "porter",
	service.SetupRegister: "登记你的加密钥匙", service.SetupName: "服务器名", service.SetupRecord: "记录到本机",
	service.SetupDaily: "日常钥匙可用",
}

func (c *cli) printStep(s service.SetupStep) {
	fmt.Fprintf(c.out, "  ✓ %s", setupLabels[s.Step])
	if s.Detail != "" {
		fmt.Fprintf(c.out, "：%s", s.Detail)
	}
	fmt.Fprintln(c.out)
}

// adminPassphrase asks for the admin passphrase when an admin key must be
// unlocked.
func (c *cli) adminPassphrase(host string) (string, error) {
	p, err := c.passphrase(host + " 的管理口令：")
	if err != nil {
		return "", valise.New("admin_locked", "这一步需要开发者模式，但没有读到 "+host+" 的管理口令",
			"在终端里运行并按提示输入；脚本中可以从标准输入传入管理口令")
	}
	return p, nil
}

// ownerPassphrase reads the Valise passphrase that proves the owner
// (docs/ARCHITECTURE.md 2.2).
func (c *cli) ownerPassphrase(host string) (string, error) {
	p, err := c.passphrase("Valise 口令（确认是你本人，" + host + "）：")
	if err != nil {
		return "", valise.New("owner_locked", "这一步需要确认是你本人，但没有读到 Valise 口令",
			"在终端里运行并按提示输入；脚本中可以从标准输入传入")
	}
	return p, nil
}

// newAdminPassphrase asks for a new admin passphrase twice.
func (c *cli) newAdminPassphrase() (string, error) {
	fmt.Fprintln(c.out, "管理口令保护管理钥匙，至少 12 个字符。可以使用 Valise 口令。")
	p1, err := c.passphrase("设置管理口令：")
	if err != nil {
		return "", err
	}
	p2, err := c.passphrase("再次输入管理口令：")
	if err != nil {
		return "", err
	}
	if p1 != p2 {
		return "", valise.New("passphrase_mismatch", "两次输入的管理口令不一致", "重新运行命令并输入相同的口令")
	}
	return p1, nil
}

// serverAddByHand: valise server add <别名> --host <地址> [...]
func (c *cli) serverAddByHand(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("server add", flag.ContinueOnError)
	host := fs.String("host", "", "")
	port := fs.Int("port", 22, "")
	user := fs.String("user", "", "")
	login := fs.String("login", config.LoginPassword, "")
	adminKey := fs.Bool("admin-key", false, "")
	trust := fs.String("trust-fingerprint", "", "")
	name := fs.String("name", "", "")
	release := fs.String("release", "", "")
	local := fs.String("local", "", "")
	pos, err := parseArgs(fs, args, 1)
	if err != nil {
		return err
	}
	srv := config.Server{Name: pos[0], HostName: *host, Port: *port, User: *user, Login: config.LoginPassword}
	if k, ok := strings.CutPrefix(*login, "key:"); ok {
		srv.Login, srv.LoginKey = config.LoginKey, k
	} else if *login != config.LoginPassword {
		return errUsage
	}
	keys, err := c.svc.ScanHostKeys(ctx, srv)
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "第一次连接 %s，Valise 无法自动验证这台服务器的身份。请与服务商控制台显示的指纹比对：\n", *host)
	for _, k := range keys {
		fmt.Fprintf(c.out, "  %s  %s\n", k.Type, k.Fingerprint)
	}
	switch {
	case *trust != "":
		if !hasFingerprint(keys, *trust) {
			return valise.New("fingerprint_mismatch", "服务器给出的指纹里没有 "+*trust, "核对指纹；不一致时不要继续")
		}
	case !c.confirm("指纹一致，信任这台服务器？"):
		return valise.New("cancelled", "已取消，没有连接这台服务器", "")
	}
	if err := c.svc.TrustHostKeys(ctx, srv, keys); err != nil {
		return err
	}
	var pass string
	if *adminKey {
		if pass, err = c.newAdminPassphrase(); err != nil {
			return err
		}
	}
	if srv.Login == config.LoginPassword {
		fmt.Fprintln(c.out, "接下来 OpenSSH 会询问一次服务器密码（Valise 不保存它）。之后日常使用不再需要密码。")
	}
	err = c.svc.AddServer(ctx, service.AddServerOptions{Server: srv, AdminPassphrase: pass,
		Setup: service.SetupOptions{Name: *name, Deploy: service.DeploySource{ReleaseTag: *release, LocalPath: *local}, Step: c.printStep}})
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "已添加 %s。日常操作用日常钥匙；需要开发者模式时，", srv.Name)
	if *adminKey {
		fmt.Fprintln(c.out, "Valise 会询问管理口令。")
	} else {
		fmt.Fprintf(c.out, "每次都要输入服务器密码（经常用可以创建管理钥匙：valise server admin-key %s）。\n", srv.Name)
	}
	return nil
}

func hasFingerprint(keys []service.HostKey, fp string) bool {
	for _, k := range keys {
		if k.Fingerprint == fp {
			return true
		}
	}
	return false
}

// serverAdminKey: valise server admin-key <服务器>
func (c *cli) serverAdminKey(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("server admin-key", flag.ContinueOnError)
	remove := fs.Bool("remove", false, "")
	pos, err := parseArgs(fs, args, 1)
	if err != nil {
		return err
	}
	if *remove {
		if !c.confirm("移除 " + pos[0] + " 的管理钥匙？之后每次管理操作都要输入服务器密码，随时可以重新创建") {
			return valise.New("cancelled", "已取消，没有任何改动", "")
		}
		if err := c.svc.RemoveAdminKey(ctx, pos[0]); err != nil {
			return err
		}
		fmt.Fprintf(c.out, "已移除 %s 的管理钥匙（服务器上和这台电脑上都删掉了）。\n", pos[0])
		return nil
	}
	pass, err := c.newAdminPassphrase()
	if err != nil {
		return err
	}
	fmt.Fprintln(c.out, "安装管理钥匙需要用服务器密码（或你的私钥）登录一次。")
	if err := c.svc.CreateAdminKey(ctx, pos[0], pass); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "已为 %s 创建管理钥匙。以后需要开发者模式时输入管理口令，%d 分钟内不再询问（仅限同一个 Valise 进程）。\n", pos[0], int(c.svc.AdminWindow().Minutes()))
	return nil
}

// serverMigrate: valise server migrate <别名> [--admin-key]
func (c *cli) serverMigrate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("server migrate", flag.ContinueOnError)
	adminKey := fs.Bool("admin-key", false, "")
	pos, err := parseArgs(fs, args, 1)
	if err != nil {
		return err
	}
	var pass string
	if *adminKey {
		if pass, err = c.newAdminPassphrase(); err != nil {
			return err
		}
	}
	missing, err := c.svc.MigrateServer(ctx, pos[0], pass)
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "%s 已改用 Valise 日常钥匙：日常操作拿不到 shell，管理操作需要管理口令或服务器密码。你自己的 ~/.ssh/config 没有改动。\n", pos[0])
	if missing {
		fmt.Fprintf(c.out, "下一步：部署 porter，valise server setup %s --release <版本>（或 --local <文件>）\n", pos[0])
	}
	return nil
}

// admin: valise admin passphrase
func (c *cli) admin(ctx context.Context, args []string) error {
	if len(args) != 1 || args[0] != "passphrase" {
		return errUsage
	}
	old, err := c.passphrase("现在的管理口令：")
	if err != nil {
		return err
	}
	pass, err := c.newAdminPassphrase()
	if err != nil {
		return err
	}
	n, err := c.svc.ChangeAdminPassphrase(ctx, old, pass)
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "已用新的管理口令重新加密 %d 把管理钥匙。\n", n)
	return nil
}

// serverList prints the recorded servers with how they are reached.
func (c *cli) serverList() error {
	list, err := c.svc.ServerList()
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Fprintln(c.out, "还没有已记录的服务器。手动添加：valise server add <别名> --host <地址>；或从 ~/.ssh/config 导入：valise server import。")
	}
	for _, s := range list {
		how := "你自己的 SSH 设置"
		if s.Valise() {
			how = "Valise 日常钥匙"
			if c.svc.HasAdminKey(s.Name) {
				how += " + 管理钥匙"
			} else {
				how += "（管理操作用服务器密码）"
			}
		}
		fmt.Fprintf(c.out, "  %-16s %s\n", s.Name, how)
	}
	return nil
}

// porterCheck: valise porter check <服务器>
func (c *cli) porterCheck(ctx context.Context, args []string) error {
	pos, err := parseArgs(flag.NewFlagSet("porter check", flag.ContinueOnError), args, 1)
	if err != nil {
		return err
	}
	u, err := c.svc.CheckPorterUpdate(ctx, pos[0])
	if err != nil {
		return err
	}
	cur := u.Current
	if cur == "" {
		cur = "还没有部署"
	}
	switch {
	case u.NoReleaseKey:
		fmt.Fprintf(c.out, "%s 上的 porter：%s。这是开发构建，没有发布签名公钥，无法检查或从 Release 更新；可以用 porter deploy --local 上传本地构建。\n", pos[0], cur)
	case u.Available:
		fmt.Fprintf(c.out, "%s 上的 porter：%s，有新版本 %s。更新：valise porter deploy %s --release %s\n", pos[0], cur, u.Latest, pos[0], u.Latest)
	default:
		fmt.Fprintf(c.out, "%s 上的 porter：%s，已是最新。\n", pos[0], cur)
	}
	return nil
}
