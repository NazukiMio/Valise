package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"valise/core/valise"
	"valise/ui/service"
)

// changePort: the two-sided port change of valise open --change-port
// (docs/LOGIN_RELAY.md section 4.2).
func (c *cli) changePort(ctx context.Context, host, instance, entry string, to int, noSeal, yes bool) error {
	p, err := c.svc.PlanPortChange(ctx, host, instance, entry, to)
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "两边一起改端口：会把服务器上 %s 的管理页端口从 %d 改成 %d", p.ProfileID, p.From, p.To)
	if p.Restart != "" {
		fmt.Fprintf(c.out, "，并重启服务 %s", p.Restart)
	}
	fmt.Fprintf(c.out, "；本机也用 %d。\n", p.To)
	fmt.Fprintf(c.out, "服务会中断一会儿（最多约 %d 秒）；以后打开管理页都用新端口。\n", int(p.Grace.Seconds()))
	if p.Draft {
		fmt.Fprintln(c.out, "⚠ 这个 harness 资料未核实，改端口的命令可能不准确。")
	}
	fmt.Fprintln(c.out, "将在服务器上运行 harness 自己的命令（需要开发者模式）：")
	for _, st := range p.Steps {
		fmt.Fprintf(c.out, "  %s\n", strings.Join(st, " "))
	}
	if noSeal {
		fmt.Fprintln(c.out, "⚠ 已选择跳过备份：出了问题时没有改动之前的配置可以恢复。")
	} else {
		fmt.Fprintln(c.out, "改动之前先建立一个备份；出了问题可以从它恢复当前实例。")
	}
	fmt.Fprintf(c.out, "想改回去：再运行 valise open %s %s --change-port --to %d\n", host, instance, p.From)
	if !yes && !c.confirm(fmt.Sprintf("把端口改成 %d？", p.To)) {
		return valise.New("cancelled", "已取消，服务器上没有任何改动", "")
	}
	return c.svc.ChangeEntryPort(ctx, p, service.PortChangeOptions{SkipSeal: noSeal,
		IO:       service.InstallIO{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr},
		Progress: func(msg string) { fmt.Fprintf(c.out, "[Valise] %s\n", msg) }})
}
