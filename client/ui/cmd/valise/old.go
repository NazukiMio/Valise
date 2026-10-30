package main

import (
	"context"
	"flag"
	"fmt"
)

// old lists or cleans up instances superseded by a migrate or clone.
func (c *cli) old(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("old", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "")
	switch args[0] {
	case "list":
		pos, err := parseArgs(fs, args[1:], 1)
		if err != nil {
			return err
		}
		res, err := c.svc.OldList(ctx, pos[0])
		if err != nil {
			return err
		}
		if len(res.Entries) == 0 {
			fmt.Fprintln(c.out, "没有被取代的旧实例。")
		}
		for _, e := range res.Entries {
			s := e.Superseded
			fmt.Fprintf(c.out, "  %s（已被取代）：%s 由 %s 取代，操作 %s\n", e.InstanceID, s.At.Local().Format("2006-01-02 15:04"), s.By, s.OpID)
			for _, l := range e.Leftovers {
				fmt.Fprintf(c.out, "    保留的旧目录：%s  %s\n", l.Path, size(l.Bytes))
			}
			if len(e.Leftovers) == 0 {
				fmt.Fprintln(c.out, "    保留的旧目录：无（已删除）")
			}
		}
		return nil
	case "cleanup":
		pos, err := parseArgs(fs, args[1:], 2)
		if err != nil {
			return err
		}
		fmt.Fprintln(c.out, "将删除旧实例 "+pos[1]+" 的记录、profile 副本，以及取代它的那次操作保留的旧目录。")
		fmt.Fprintln(c.out, "金库中这个实例的快照不受影响，仍然可以用来复活。")
		if !*yes && !c.confirm("永久删除这些内容？") {
			return nil
		}
		removed, err := c.svc.OldCleanup(ctx, pos[0], pos[1])
		for _, r := range removed {
			fmt.Fprintf(c.out, "已删除 %s\n", r)
		}
		return err
	}
	return errUsage
}
