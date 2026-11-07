package main

import (
	"context"
	"flag"
	"fmt"
)

// protectionResolve settles a plan change a crash or a failed check left
// unsettled: valise protection resolve <服务器> <实例> --verify|--rollback.
func (c *cli) protectionResolve(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("protection resolve", flag.ContinueOnError)
	verify := fs.Bool("verify", false, "")
	rollback := fs.Bool("rollback", false, "")
	pos, err := parseArgs(fs, args, 2)
	if err != nil {
		return err
	}
	if *verify == *rollback {
		return errUsage
	}
	action := "verify"
	if *rollback {
		action = "rollback"
	}
	ch, err := c.svc.ResolveProtection(ctx, pos[0], pos[1], action)
	if err != nil {
		return err
	}
	switch {
	case ch.ReconcileRequired:
		fmt.Fprintf(c.out, "仍未确认：服务器上的设置还不能核实（%s）。按计划备份继续暂停；检查定时器后再运行一次。\n", ch.ErrorCode)
	case ch.Committed:
		fmt.Fprintf(c.out, "已确认：新设置（版本 %d）生效，按计划备份恢复。\n", uint64(ch.NewRevision))
	case ch.RollbackSucceeded:
		fmt.Fprintf(c.out, "已放弃这次修改，恢复旧设置（版本 %d），按计划备份恢复。\n", uint64(ch.OldRevision))
	default:
		fmt.Fprintf(c.out, "结果：%s\n", ch.State)
	}
	return nil
}
