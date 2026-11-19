package main

import (
	"context"
	"flag"
	"fmt"

	"valise/core/kittools"
	"valise/ui/service"
)

// Cloud drives (docs/ARCHITECTURE.md 7.3).

func (c *cli) cloud(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errUsage
	}
	switch args[0] {
	case "list":
		return c.cloudList()
	case "rclone":
		return c.cloudRclone(ctx)
	case "add":
		return c.cloudAdd(ctx, args[1:])
	case "remove":
		return c.cloudRemove(ctx, args[1:])
	}
	return errUsage
}

func (c *cli) cloudList() error {
	list, err := c.svc.Clouds()
	if err != nil {
		return err
	}
	fmt.Fprintln(c.out, "可以添加的云盘（经 rclone）：")
	for _, cl := range list {
		state := ""
		if cl.Pending() {
			state = "（还没有在这个版本上测试）"
		}
		fmt.Fprintf(c.out, "  %-10s %s：%s%s\n", cl.ID, cl.Name, cl.AccessNote, state)
	}
	if len(c.svc.Config.Clouds) > 0 {
		fmt.Fprintln(c.out, "在这台电脑登录的云盘（只在 Valise 打开时更新）：")
		for _, h := range c.svc.Config.Clouds {
			fmt.Fprintf(c.out, "  %-10s %s\n", h.Name, h.Cloud)
		}
	}
	return nil
}

// cloudRclone gets rclone ready on this computer.
func (c *cli) cloudRclone(ctx context.Context) error {
	if l := c.svc.FindLocalRclone(ctx); l != nil {
		where := "PATH 中"
		if l.Ours {
			where = "Valise 下载的"
		}
		fmt.Fprintf(c.out, "这台电脑使用%s的 rclone：%s（%s）\n", where, l.Path, l.Version)
		return nil
	}
	d, err := c.svc.PlanLocalRclone(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "这台电脑没有 rclone。可以下载 Valise 清单锁定的 rclone %s：\n  地址：%s\n  SHA-256：%s\n", d.Version, d.URL, d.SHA256)
	if d.Bytes > 0 {
		fmt.Fprintf(c.out, "  大小：%s\n", size(d.Bytes))
	}
	fmt.Fprintln(c.out, "  放在 Valise 自己的目录里，不改你的 PATH，也不碰你自己的 rclone 配置。")
	if !c.confirm("下载并核对？") {
		return nil
	}
	l, err := c.svc.InstallLocalRclone(ctx, d)
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "已准备：%s（%s），SHA-256 与清单一致。\n", l.Path, l.Version)
	return nil
}

func (c *cli) cloudAdd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("cloud add", flag.ContinueOnError)
	id := fs.String("cloud", "", "")
	bucket := fs.String("bucket", "", "")
	pending := fs.Bool("allow-pending", false, "")
	headless := fs.Bool("headless", false, "")
	here := fs.Bool("here", false, "")
	if len(args) > 0 && args[0] == "--here" {
		return c.cloudAddHere(ctx, args[1:])
	}
	pos, err := parseArgs(fs, args, 2)
	if err != nil {
		return err
	}
	if *here {
		return c.cloudAddHere(ctx, args)
	}
	host, name := pos[0], pos[1]
	list, err := c.svc.Clouds()
	if err != nil {
		return err
	}
	var cl *kittools.Cloud
	for i := range list {
		if list[i].ID == *id {
			cl = &list[i]
		}
	}
	if cl == nil {
		return fmt.Errorf("没有指定云盘或清单里没有 %q；运行 valise cloud list 查看", *id)
	}
	fmt.Fprintf(c.out, "在服务器 %s 上登录 %s，作为备份位置 %s。\n", host, cl.Name, name)
	fmt.Fprintln(c.out, "  · 授权保存在服务器上（Valise 自己的 rclone 配置）。服务器被攻破时，这个授权会一起暴露；备份内容本身是加密的。")
	fmt.Fprintln(c.out, "  · 授权范围："+cl.AccessNote+"。")
	if cl.Access == kittools.AccessAccount {
		fmt.Fprintln(c.out, "  · 这个授权能访问整个网盘。更稳妥的做法是只在这台电脑登录，或改用 Google Drive。")
	}
	fmt.Fprintln(c.out, "  · 网盘里能看到文件夹名（服务器名、实例名、周期）与文件大小；文件内容是加密的。")
	if cl.AskApp {
		fmt.Fprintln(c.out, "  · rclone 会先问 client_id、client_secret、service_account_file 与是否编辑高级设置。")
		fmt.Fprintln(c.out, "    有自己的应用就填在 rclone 的提问里（Valise 不读取、不保存）；说明见 "+cl.AppHelp)
		fmt.Fprintln(c.out, "    client_id 留空时，rclone 会问是否继续用它的共享应用：共享应用可能随时停用，之后需要重新登录。")
	}
	fmt.Fprintln(c.out, "  · 登录在服务器的终端里进行；需要打开浏览器时，链接经隧道在这台电脑上打开（服务器上 rclone 的本地登录页，随后跳到网盘的授权页）。")
	if !c.confirm("开始登录？") {
		return nil
	}
	term := newLoginTerminal(c)
	defer term.restore()
	res, err := c.svc.CloudLoginServer(ctx, host, name, cl.ID, service.CloudOptions{AllowPending: *pending, Headless: *headless, Bucket: *bucket}, service.CloudLoginIO{
		LoginIO: service.LoginIO{
			Stdin: term.remote, Stdout: c.out, Cols: term.cols, Rows: term.rows,
			Status: func(s service.LoginStatus) {
				switch s.State {
				case service.LoginRelayReady:
					term.say("登录授权已就绪：本机 %v 转发到服务器 %s 端口 %d", s.Local, s.Remote, s.Port)
				case service.LoginBrowserOpened:
					term.say("状态：已在本机浏览器中打开")
				case service.LoginCallbackReceived:
					term.say("状态：已收到回调")
				case service.LoginExited:
					term.say("状态：rclone 已退出，退出码 %d", s.ExitCode)
				}
			},
			ConfirmOpen: func(link, domain string) bool {
				return term.confirm(fmt.Sprintf("要打开的是 %s 上的登录页。按回车在本机浏览器中打开，按 n 跳过：", domain))
			},
		},
		ConfirmAuthorize: func(line string) bool {
			return term.confirm("服务器要求在有浏览器的电脑上运行：" + line + "\r\n按回车用这台电脑的 rclone 运行（结果只显示在这里，由你复制粘贴回服务器），按 n 跳过：")
		},
	})
	term.restore()
	if err != nil {
		return err
	}
	how := "经隧道在本机浏览器授权"
	if res.Headless {
		how = "用 rclone 的无浏览器授权"
	}
	fmt.Fprintf(c.out, "已登录（%s），连接测试通过，已保存备份位置 %s。\n现在可以复制：valise sync %s %s\n", how, name, host, name)
	return nil
}

// cloudAddHere logs this computer in: rclone's own configuration runs in
// this terminal and opens this computer's browser.
func (c *cli) cloudAddHere(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("cloud add --here", flag.ContinueOnError)
	id := fs.String("cloud", "", "")
	bucket := fs.String("bucket", "", "")
	pending := fs.Bool("allow-pending", false, "")
	fs.Bool("here", true, "")
	pos, err := parseArgs(fs, args, 1)
	if err != nil {
		return err
	}
	if c.svc.FindLocalRclone(ctx) == nil {
		if err := c.cloudRclone(ctx); err != nil {
			return err
		}
		if c.svc.FindLocalRclone(ctx) == nil {
			return nil
		}
	}
	fmt.Fprintf(c.out, "在这台电脑上登录云盘，作为备份位置 %s。授权留在这台电脑（Valise 自己的 rclone 配置）；复制经这台电脑中转，只在 Valise 打开时进行。\n", pos[0])
	fmt.Fprintln(c.out, "接下来是 rclone 自己的提问，会在这台电脑的浏览器里打开授权页。")
	if err := c.svc.CloudLoginHere(ctx, pos[0], *id, service.CloudOptions{AllowPending: *pending, Bucket: *bucket}, c.in, c.out); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "已登录，连接测试通过。复制：valise sync <服务器> %s\n", pos[0])
	return nil
}

func (c *cli) cloudRemove(ctx context.Context, args []string) error {
	if len(args) == 2 && args[0] == "--here" {
		if !c.confirm("移除这台电脑上的云盘登录 " + args[1] + "？网盘里已有的备份不会删除。") {
			return nil
		}
		if err := c.svc.CloudRemoveHere(ctx, args[1]); err != nil {
			return err
		}
		fmt.Fprintln(c.out, "已移除这台电脑上的登录；网盘账户里的授权需要你在账户设置里撤销。")
		return nil
	}
	pos, err := parseArgs(flag.NewFlagSet("cloud remove", flag.ContinueOnError), args, 2)
	if err != nil {
		return err
	}
	if !c.confirm(fmt.Sprintf("移除服务器 %s 上的云盘位置 %s（连同服务器上的登录）？网盘里已有的备份不会删除。", pos[0], pos[1])) {
		return nil
	}
	if err := c.svc.CloudRemove(ctx, pos[0], pos[1]); err != nil {
		return err
	}
	fmt.Fprintln(c.out, "已移除。服务器上的授权已删除；网盘账户里的授权需要你在账户设置里撤销。")
	return nil
}
