package main

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"valise/ui/service"
)

// server maintains the recorded servers (SSH aliases only).
func (c *cli) server(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errUsage
	}
	switch args[0] {
	case "name":
		if len(args) != 3 {
			return errUsage
		}
		if err := c.svc.ServerNameSet(ctx, args[1], args[2]); err != nil {
			return err
		}
		fmt.Fprintf(c.out, "%s 的服务器名是 %s（云备份中的文件夹名，之后不能修改）。\n", args[1], args[2])
		return nil
	case "setup":
		fs := flag.NewFlagSet("server setup", flag.ContinueOnError)
		name := fs.String("name", "", "")
		release := fs.String("release", "", "")
		local := fs.String("local", "", "")
		pos, err := parseArgs(fs, args[1:], 1)
		if err != nil {
			return err
		}
		labels := map[string]string{service.SetupCheck: "检查服务器", service.SetupDeploy: "porter", service.SetupRegister: "登记你的钥匙",
			service.SetupName: "服务器名", service.SetupRecord: "记录到本机"}
		err = c.svc.SetupServer(ctx, pos[0], service.SetupOptions{Name: *name, Deploy: service.DeploySource{ReleaseTag: *release, LocalPath: *local},
			Step: func(s service.SetupStep) {
				fmt.Fprintf(c.out, "  ✓ %s", labels[s.Step])
				if s.Detail != "" {
					fmt.Fprintf(c.out, "：%s", s.Detail)
				}
				fmt.Fprintln(c.out)
			}})
		if err == nil {
			fmt.Fprintf(c.out, "已添加 %s。接入 agent：valise enroll %s\n", pos[0], pos[0])
		}
		return err
	case "list":
		return c.serverList()
	case "admin-key":
		return c.serverAdminKey(ctx, args[1:])
	case "rekey":
		return c.serverRekey(ctx, args[1:])
	case "migrate":
		return c.serverMigrate(ctx, args[1:])
	case "add":
		if len(args) < 2 {
			return errUsage
		}
		for _, a := range args[1:] {
			if a == "--host" || strings.HasPrefix(a, "--host=") || a == "-host" {
				return c.serverAddByHand(ctx, args[1:])
			}
		}
		added, err := c.svc.AddServers(args[1:]...)
		if err != nil {
			return err
		}
		if len(added) == 0 {
			fmt.Fprintln(c.out, "这些服务器都已记录。")
		} else {
			fmt.Fprintf(c.out, "已记录：%s\n", strings.Join(added, "、"))
		}
		return nil
	case "remove":
		if len(args) != 2 {
			return errUsage
		}
		ok, err := c.svc.RemoveServer(args[1])
		if err != nil {
			return err
		}
		if ok {
			fmt.Fprintf(c.out, "已不再记录 %s（服务器上的内容没有任何改动）。\n", args[1])
		} else {
			fmt.Fprintf(c.out, "%s 不在已记录的服务器中。\n", args[1])
		}
		return nil
	case "import":
		if len(args) > 1 {
			added, err := c.svc.AddServers(args[1:]...)
			if err == nil {
				fmt.Fprintf(c.out, "已记录：%s\n", strings.Join(added, "、"))
			}
			return err
		}
		hosts, err := c.svc.ImportableHosts()
		if err != nil {
			return err
		}
		if len(hosts) == 0 {
			fmt.Fprintln(c.out, "~/.ssh/config 中没有尚未记录的具体 Host。")
			return nil
		}
		fmt.Fprintln(c.out, "~/.ssh/config 中尚未记录的 Host：")
		for _, h := range hosts {
			fmt.Fprintf(c.out, "  %s\n", h)
		}
		fmt.Fprintln(c.out, "记录其中的服务器：valise server import <别名>…")
		return nil
	}
	return errUsage
}
