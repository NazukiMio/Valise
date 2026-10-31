package main

import (
	"fmt"
	"strings"
)

// server maintains the recorded servers (SSH aliases only).
func (c *cli) server(args []string) error {
	if len(args) == 0 {
		return errUsage
	}
	switch args[0] {
	case "list":
		list, err := c.svc.Servers()
		if err != nil {
			return err
		}
		if len(list) == 0 {
			fmt.Fprintln(c.out, "还没有已记录的服务器。用 valise server add <别名> 或 valise server import 添加。")
		}
		for _, s := range list {
			fmt.Fprintf(c.out, "  %s\n", s)
		}
		return nil
	case "add":
		if len(args) < 2 {
			return errUsage
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
