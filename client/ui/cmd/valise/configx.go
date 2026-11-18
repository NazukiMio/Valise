package main

import (
	"context"
	"flag"
	"fmt"
	"strconv"
	"strings"

	"valise/client/config"
	"valise/core/valise"
	"valise/ui/service"
)

// configCmd: valise config export|import <文件> (docs/ARCHITECTURE.md 3.1).
func (c *cli) configCmd(op, path string) error {
	switch op {
	case "red-dismiss-days":
		n, err := strconv.Atoi(path)
		if err != nil {
			return errUsage
		}
		if err := c.svc.SetRedDismissDays(n); err != nil {
			return err
		}
		fmt.Fprintf(c.out, "以后忽略红色提醒时，主页 %d 天后再提醒（健康页里照常显示红色）。\n", n)
		return nil
	case "detach":
		if path != "on" && path != "off" {
			return errUsage
		}
		if err := c.svc.SetDetachedTasks(path == "on"); err != nil {
			return err
		}
		if path == "on" {
			fmt.Fprintln(c.out, "分离模式已开启：服务器支持时，按计划备份交给服务器自己的一次性任务执行，关掉 Valise 也会继续。")
		} else {
			fmt.Fprintln(c.out, "分离模式已关闭：按计划备份在连接里执行，关掉 Valise 会中断正在进行的备份。")
		}
		return nil
	case "export":
		r, err := c.svc.ExportConfig(path)
		if err != nil {
			return err
		}
		fmt.Fprintf(c.out, "已导出到 %s：%d 台服务器、%d 条主机指纹和设置。\n", r.Path, r.Servers, r.KnownHosts)
		fmt.Fprintln(c.out, "文件里没有任何钥匙，但有服务器地址和用户名，请不要公开分享。")
		fmt.Fprintln(c.out, "加密钥匙请另外用「备份钥匙」导出恢复套件；日常钥匙和管理钥匙不会离开这台电脑。")
		return nil
	case "import":
		r, err := c.svc.ImportConfig(path)
		if err != nil {
			return err
		}
		for _, l := range importSummary(r) {
			fmt.Fprintln(c.out, l)
		}
		return nil
	}
	return errUsage
}

// configTakeover: valise config takeover <服务器> on|off
// (docs/SERVICE_CONTRACTS.md 16.F2). It only changes how the
// terminal UI runs a sudo fix; valise health fix itself is unchanged.
func (c *cli) configTakeover(host, state string) error {
	var on bool
	switch state {
	case "on":
		on = true
	case "off":
	default:
		return errUsage
	}
	if err := c.svc.SetTakeover(host, on); err != nil {
		return err
	}
	if on {
		fmt.Fprintf(c.out, "%s 的接管模式已开启：终端界面里需要 sudo 的修复，在你验证口令并确认开启高级模式后用 sudo -n 直接执行；sudo 需要密码时仍转到终端。\n", host)
	} else {
		fmt.Fprintf(c.out, "%s 的接管模式已关闭：需要 sudo 的修复转到终端执行。\n", host)
	}
	return nil
}

// importSummary is the plain report of an import, shared with the TUI.
func importSummary(r *service.ConfigImportResult) []string {
	var out []string
	if len(r.Added) == 0 {
		out = append(out, "没有新的服务器需要导入。")
	} else {
		out = append(out, "已导入："+strings.Join(r.Added, "、"))
	}
	if len(r.Same) > 0 {
		out = append(out, "已经有了（内容相同）："+strings.Join(r.Same, "、"))
	}
	if len(r.Skipped) > 0 {
		out = append(out, "跳过（这台电脑上已有同名服务器，内容不同，没有覆盖）："+strings.Join(r.Skipped, "、"))
	}
	if r.Settings != "" {
		out = append(out, "设置："+r.Settings)
	}
	for _, h := range r.NeedKeys {
		out = append(out, "  "+h+"：还没有这台电脑的日常钥匙，运行 valise server rekey "+h+"（需要服务器密码登录一次）")
	}
	if len(r.NeedSSHConfig) > 0 {
		out = append(out, "  这些服务器使用你自己的 SSH 设置，请确认这台电脑的 ~/.ssh/config 里有同名的 Host："+strings.Join(r.NeedSSHConfig, "、"))
	}
	return out
}

// serverRekey: valise server rekey <服务器> [--admin-key] installs this
// computer's own daily key on a server already in the list.
func (c *cli) serverRekey(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("server rekey", flag.ContinueOnError)
	adminKey := fs.Bool("admin-key", false, "")
	pos, err := parseArgs(fs, args, 1)
	if err != nil {
		return err
	}
	srv, ok, err := config.FindServer(c.svc.Dirs.Data, pos[0])
	if err != nil {
		return err
	}
	if !ok {
		return valise.New("server_unknown", pos[0]+" 不在已记录的服务器中", "valise server list 查看已记录的服务器")
	}
	if !srv.Valise() {
		return valise.New("server_own_ssh", pos[0]+" 使用你自己的 SSH 设置，没有 Valise 的日常钥匙", "")
	}
	var pass string
	if *adminKey {
		if pass, err = c.newAdminPassphrase(); err != nil {
			return err
		}
	}
	if srv.Login == config.LoginPassword {
		fmt.Fprintln(c.out, "接下来 OpenSSH 会询问一次服务器密码（Valise 不保存它）。")
	}
	err = c.svc.AddServer(ctx, service.AddServerOptions{Server: srv, AdminPassphrase: pass, Setup: service.SetupOptions{Step: c.printStep}})
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "已在 %s 上安装这台电脑的日常钥匙。\n", srv.Name)
	return nil
}
