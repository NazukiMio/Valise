package main

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"valise/ui/service"
)

// forward: valise forward <服务器> <地址|端口> [--idle 30m] [--no-browser]
// (docs/LOGIN_RELAY.md section 6).
func (c *cli) forward(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("forward", flag.ContinueOnError)
	idle := fs.Duration("idle", service.DefaultEntryIdle, "")
	noBrowser := fs.Bool("no-browser", false, "")
	pos, err := parseArgs(fs, args, 2)
	if err != nil {
		return err
	}
	ft, err := service.ParseForward(pos[1])
	if err != nil {
		return err
	}
	steps := map[string]string{service.EntryTunnel: "隧道已建立", service.EntryRemote: "远端端口在监听", service.EntryHealth: "页面可以访问"}
	err = c.svc.ForwardPage(ctx, pos[0], ft, service.OpenOptions{Idle: *idle,
		Status: func(s service.EntryStatus) {
			mark := "✓"
			if !s.OK {
				mark = "✗"
			}
			fmt.Fprintf(c.out, "  %s %s：%s\n", mark, steps[s.Step], s.Detail)
		},
		Ready: func(open, display string) {
			fmt.Fprintf(c.out, "页面：%s（完整地址只交给浏览器，不显示）\n", display)
			if !*noBrowser {
				if err := service.OpenURL(open); err != nil {
					fmt.Fprintln(c.out, "无法打开浏览器。")
				}
			}
			how := "按 Ctrl-C 结束转发"
			if *idle > 0 {
				how += fmt.Sprintf("；%s 内没有任何连接会自动结束", strings.TrimSuffix((*idle).String(), "0s"))
			}
			fmt.Fprintln(c.out, how+"。登录类页面不一定都能这样完成；不行时改用 valise login 或原生界面。")
		}})
	if err == nil {
		fmt.Fprintln(c.out, "转发已结束。")
	}
	return err
}
