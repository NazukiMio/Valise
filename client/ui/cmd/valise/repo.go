package main

import (
	"context"
	"fmt"
	"valise/ui/service"
)

// repo: valise repo add|list|remove|sync (docs/ARCHITECTURE.md section 7,
// "本机仓库").
func (c *cli) repo(ctx context.Context, args []string) error {
	switch {
	case args[0] == "add" && len(args) == 3:
		r, err := c.svc.RepoAdd(args[1], args[2])
		if err != nil {
			return err
		}
		fmt.Fprintf(c.out, "已添加仓库 %s：%s\n", r.Name, r.Path)
		fmt.Fprintln(c.out, "备份会在你同步、打开 Valise 或在这台电脑上建立备份之后补到这里（可用 valise protection copies 按实例调整）；Valise 不常驻，关着的时候不会更新。")
		return nil
	case args[0] == "list" && len(args) == 1:
		if len(c.svc.Repos()) == 0 {
			fmt.Fprintln(c.out, "还没有仓库。添加：valise repo add <名称> <文件夹>")
		}
		for _, r := range c.svc.Repos() {
			fmt.Fprintf(c.out, "%s\t%s\n", r.Name, r.Path)
		}
		return nil
	case args[0] == "remove" && len(args) == 2:
		if err := c.svc.RepoRemove(args[1]); err != nil {
			return err
		}
		fmt.Fprintf(c.out, "已从列表中移除仓库 %s；文件夹里的备份没有删除。\n", args[1])
		return nil
	case args[0] == "sync" && (len(args) == 2 || len(args) == 3):
		names := []string{}
		if len(args) == 3 {
			names = append(names, args[2])
		} else {
			for _, r := range c.svc.Repos() {
				names = append(names, r.Name)
			}
		}
		if len(names) == 0 {
			fmt.Fprintln(c.out, "还没有仓库。添加：valise repo add <名称> <文件夹>")
			return nil
		}
		for _, n := range names {
			res, err := c.svc.RepoSync(c.withProgress(ctx), args[1], n)
			if err != nil {
				return err
			}
			fmt.Fprintf(c.out, "仓库 %s（%s）：%d 个周期、%d 个备份，这次新复制 %d 个文件，校验通过。\n", n, res.Dir, len(res.Leaves), len(res.Snapshots), res.Downloaded)
			if t := service.CheckSummary(res.Checks); t != "" {
				fmt.Fprintln(c.out, "  "+t)
			}
		}
		return nil
	}
	return errUsage
}
