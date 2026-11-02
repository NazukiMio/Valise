package main

import (
	"context"
	"flag"
	"fmt"

	"valise/core/kittools"
	"valise/core/valise"
	"valise/ui/service"
)

var kitTierNames = map[string]string{kittools.TierEnhanced: "增强", kittools.TierExpert: "专家"}

// kitTools: valise kit tools <服务器> | kit tool install|uninstall <服务器> <名称> [--yes]
func (c *cli) kitTools(ctx context.Context, args []string) error {
	pos, err := parseArgs(flag.NewFlagSet("kit tools", flag.ContinueOnError), args, 1)
	if err != nil {
		return err
	}
	list, err := c.svc.KitTools(ctx, pos[0])
	if err != nil {
		return err
	}
	for _, k := range list {
		state := "没有"
		if k.Present {
			state = "已有"
			if k.Ours {
				state = "已有（Valise 装的）"
			}
		}
		fmt.Fprintf(c.out, "%s  %s · %s\n  用途：%s\n  不装会怎样：%s\n", k.ID, kitTierNames[k.Tier], state, k.Purpose, k.Without)
		switch {
		case k.Present:
		case k.Why != "":
			fmt.Fprintf(c.out, "  不能由 Valise 安装：%s\n", k.Why)
		case k.Tier == kittools.TierExpert:
			fmt.Fprintf(c.out, "  安装（高级模式）：valise kit tool install %s %s\n", pos[0], k.ID)
		default:
			fmt.Fprintf(c.out, "  安装：valise kit tool install %s %s\n", pos[0], k.ID)
		}
	}
	return nil
}

func (c *cli) kitTool(ctx context.Context, args []string) error {
	if len(args) == 0 || (args[0] != "install" && args[0] != "uninstall") {
		return errUsage
	}
	uninstall := args[0] == "uninstall"
	fs := flag.NewFlagSet("kit tool", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "")
	pos, err := parseArgs(fs, args[1:], 2)
	if err != nil {
		return err
	}
	host, id := pos[0], pos[1]
	list, err := c.svc.KitTools(ctx, host)
	if err != nil {
		return err
	}
	var tier string
	for _, k := range list {
		if k.ID == id {
			tier = k.Tier
		}
	}
	switch tier {
	case kittools.TierEnhanced:
		if uninstall {
			if !*yes && !c.confirm("删除 Valise 装的 "+id+"？") {
				return valise.New("cancelled", "已取消，服务器上没有任何改动", "")
			}
			if err := c.svc.RemoveKitBinary(ctx, host, id); err != nil {
				return err
			}
			fmt.Fprintf(c.out, "已删除 %s。\n", id)
			return nil
		}
		if !*yes && !c.confirm("下载 "+id+" 的官方版本，核对 SHA-256 后装到服务器的 ~/.local/bin？") {
			return valise.New("cancelled", "已取消，服务器上没有任何改动", "")
		}
		k, err := c.svc.InstallKitBinary(ctx, host, id)
		if err != nil {
			return err
		}
		fmt.Fprintf(c.out, "已安装 %s %s：%s\n", k.Name, k.Binary.Version, k.Path)
		return nil
	case kittools.TierExpert:
		if err := c.svc.AdvancedReady(host); err != nil {
			return err
		}
		if err := c.gate(ctx, host, "", *yes); err != nil {
			return err
		}
		k, batch, err := c.svc.KitToolBatch(ctx, host, id, uninstall)
		if err != nil {
			return err
		}
		what := "安装命令（来源：Valise Kit 清单）"
		if uninstall {
			what = "卸载命令（来源：Valise Kit 清单）"
		}
		if err := c.withAdvanced(ctx, host, what, batch, *yes, func(io service.InstallIO) error {
			return c.svc.RunKitToolBatch(ctx, host, batch, io)
		}); err != nil {
			return err
		}
		fmt.Fprintf(c.out, "完成：%s。\n", k.Name)
		return nil
	}
	return valise.New("kit_tool_unknown", "Kit 清单里没有 "+id, "运行 valise kit tools "+host+" 查看清单")
}
