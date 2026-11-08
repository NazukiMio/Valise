package main

import (
	"context"
	"flag"
	"fmt"

	"valise/ui/service"
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

// protectionCopies shows or changes this client's binding for an instance
// (docs/ARCHITECTURE.md 7.4):
// valise protection copies <服务器> <实例> [--repo 名 [--after-backup on|off] [--on-open on|off]]...
func (c *cli) protectionCopies(ctx context.Context, args []string) error {
	if len(args) < 2 {
		return errUsage
	}
	host, inst := args[0], args[1]
	var changes []service.LocalCopyChange
	onOff := func(v string) (*bool, error) {
		switch v {
		case "on":
			b := true
			return &b, nil
		case "off":
			b := false
			return &b, nil
		}
		return nil, errUsage
	}
	rest := args[2:]
	for len(rest) > 0 {
		if len(rest) < 2 {
			return errUsage
		}
		flagName, val := rest[0], rest[1]
		rest = rest[2:]
		if flagName == "--repo" {
			changes = append(changes, service.LocalCopyChange{Repo: val})
			continue
		}
		if len(changes) == 0 {
			return errUsage
		}
		b, err := onOff(val)
		if err != nil {
			return err
		}
		switch flagName {
		case "--after-backup":
			changes[len(changes)-1].AfterBackup = b
		case "--on-open":
			changes[len(changes)-1].OnClientOpen = b
		default:
			return errUsage
		}
	}
	if len(changes) > 0 {
		if _, err := c.svc.SetLocalCopies(ctx, host, inst, changes); err != nil {
			return err
		}
		fmt.Fprintln(c.out, "已保存本客户端的设置。")
	}
	v, err := c.svc.LocalCopies(ctx, host, inst)
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "%s 上的 %s：这台电脑什么时候把备份补到本机仓库（只在 Valise 打开或命令运行期间进行）\n", host, inst)
	switch {
	case !v.Planned:
		fmt.Fprintln(c.out, "  还没有采用保护计划：打开时与备份后都补到所有仓库。")
		return nil
	case v.Binding == nil:
		fmt.Fprintln(c.out, "  还没有设置：打开时与备份后都补到所有仓库。用 --repo 修改。")
		return nil
	}
	word := func(b *bool) string {
		if b != nil && *b {
			return "补"
		}
		return "不补"
	}
	for _, r := range c.svc.Repos() {
		after, open := "不补", "不补"
		for _, lc := range v.Binding.LocalCopies {
			if lc.RepoID == r.Name {
				after, open = word(lc.AfterBackup), word(lc.OnClientOpen)
			}
		}
		fmt.Fprintf(c.out, "  仓库 %-10s 备份后：%s　打开时：%s\n", r.Name, after, open)
	}
	fmt.Fprintf(c.out, "  设置版本 %d\n", uint64(v.Binding.Revision))
	return nil
}
