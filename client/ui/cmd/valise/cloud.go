package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"valise/core/valise"
	"valise/core/vault"
	"valise/core/vaultrpc"
)

// describeCopies prints where else a leaf exists and reports whether any
// complete copy remains besides the one being deleted.
func (c *cli) describeCopies(ctx context.Context, host, leaf, deleting string) (bool, error) {
	copies, errs, err := c.svc.LeafCopies(ctx, host, leaf)
	if err != nil {
		return false, err
	}
	other := false
	fmt.Fprintf(c.out, "周期 %s 的副本：\n", leaf)
	for _, cp := range copies {
		where := cp.Where
		if where == "server" {
			where = "服务器"
		}
		state := "完整"
		switch {
		case cp.Deleting:
			state = "删除未完成"
		case !cp.Complete:
			state = "不完整"
		}
		fmt.Fprintf(c.out, "  %s：%s\n", where, state)
		if cp.Where != deleting && cp.Complete {
			other = true
		}
	}
	for name, e := range errs {
		reason := e.Error()
		if ue, ok := valise.As(e); ok {
			reason = ue.Reason + "；" + ue.Suggestion
		}
		fmt.Fprintf(c.out, "  %s：无法列出，不知道那里是否有副本（%s）\n", name, reason)
	}
	return other, nil
}

func (c *cli) confirmDelete(ctx context.Context, host, leaf, where string, yes bool) (bool, error) {
	st, err := c.svc.VaultStatus(ctx, host)
	if err != nil {
		return false, err
	}
	for _, l := range st.Leaves {
		if l.Leaf == leaf && l.Current {
			return false, valise.New("leaf_current", "周期 "+leaf+" 是当前正在写入的周期，不能删除", "等这个周期结束后再删除")
		}
	}
	other, err := c.describeCopies(ctx, host, leaf, where)
	if err != nil {
		return false, err
	}
	if yes {
		return true, nil
	}
	if !other {
		fmt.Fprintln(c.out, "⚠ 其他地方都没有这个周期的完整副本。删除后，这些逃生舱将无法恢复。")
		if !c.confirm("确定要删除？") {
			return false, nil
		}
	}
	return c.confirm("删除周期 " + leaf + "？"), nil
}

// vaultDelete deletes one whole period on the server.
func (c *cli) vaultDelete(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("vault delete", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "")
	pos, err := parseArgs(fs, args, 2)
	if err != nil {
		return err
	}
	ok, err := c.confirmDelete(ctx, pos[0], pos[1], "server", *yes)
	if err != nil || !ok {
		return err
	}
	res, err := c.svc.LeafDelete(ctx, pos[0], pos[1])
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "已删除服务器上的周期 %s，释放 %s。云备份上的副本不受影响。\n", res.Leaf, size(res.Bytes))
	return nil
}

func (c *cli) targetList(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("target list", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "")
	pos, err := parseArgs(fs, args, 2)
	if err != nil {
		return err
	}
	list, err := c.svc.TargetList(ctx, pos[0], pos[1])
	if err != nil {
		return err
	}
	if *asJSON {
		return c.printJSON(list)
	}
	st, err := c.svc.VaultStatus(ctx, pos[0])
	if err != nil {
		return err
	}
	onServer := map[string]int{}
	for _, l := range st.Leaves {
		onServer[l.Leaf] = len(l.Snapshots)
	}
	fmt.Fprintf(c.out, "%s 上属于 %s 的周期（%s）：\n", list.Target, pos[0], list.Dest)
	seen := map[string]bool{}
	for _, l := range list.Leaves {
		seen[l.Leaf] = true
		where := "只在云端"
		if n, ok := onServer[l.Leaf]; ok {
			where = "两处都有"
			if n != len(l.Snapshots) {
				where = fmt.Sprintf("两处数量不同（服务器 %d 个）", n)
			}
		}
		if l.Deleting {
			where = "删除未完成"
		}
		fmt.Fprintf(c.out, "  %-32s %3d 个逃生舱 %10s  %s\n", l.Leaf, len(l.Snapshots), size(l.Bytes), where)
	}
	for _, l := range st.Leaves {
		if !seen[l.Leaf] && len(l.Snapshots) > 0 {
			fmt.Fprintf(c.out, "  %-32s %3d 个逃生舱 %10s  只在服务器（尚未同步）\n", l.Leaf, len(l.Snapshots), size(l.Bytes))
		}
	}
	return nil
}

func (c *cli) targetDelete(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("target delete", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "")
	pos, err := parseArgs(fs, args, 3)
	if err != nil {
		return err
	}
	ok, err := c.confirmDelete(ctx, pos[0], pos[2], pos[1], *yes)
	if err != nil || !ok {
		return err
	}
	if err := c.svc.TargetLeafDelete(ctx, pos[0], pos[1], pos[2]); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "已删除 %s 上的周期 %s。服务器上的副本不受影响。\n", pos[1], pos[2])
	return nil
}

func (c *cli) targetAuto(ctx context.Context, args []string) error {
	pos, err := parseArgs(flag.NewFlagSet("target auto", flag.ContinueOnError), args, 3)
	if err != nil {
		return err
	}
	st, err := c.svc.VaultStatus(ctx, pos[0])
	if err != nil {
		return err
	}
	var t *vaultrpc.Target
	for i := range st.Targets {
		if st.Targets[i].Name == pos[1] {
			t = &st.Targets[i].Target
		}
	}
	if t == nil {
		return fmt.Errorf("%s 上没有云备份 %s", pos[0], pos[1])
	}
	if pos[2] == "off" {
		t.Auto = nil
	} else {
		t.Auto = &vaultrpc.AutoSync{Every: pos[2]}
	}
	if err := c.svc.TargetSet(ctx, pos[0], *t); err != nil {
		return err
	}
	if t.Auto == nil {
		fmt.Fprintf(c.out, "已关闭 %s 的自动同步。\n", t.Name)
	} else {
		fmt.Fprintf(c.out, "已开启 %s 的自动同步：每次建立逃生舱后，距上次同步超过 %s 就顺带同步一次。\n", t.Name, t.Auto.Every)
	}
	return nil
}

// period sets an instance's period unit.
func (c *cli) period(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("period", flag.ContinueOnError)
	tz := fs.String("tz", localZone(), "")
	pos, err := parseArgs(fs, args, 3)
	if err != nil {
		return err
	}
	if err := c.svc.PeriodSet(ctx, pos[0], pos[1], vault.PeriodSetting{Unit: pos[2], TZ: *tz}); err != nil {
		return err
	}
	names := map[string]string{vault.UnitWeek: "每周", vault.UnitMonth: "每月", vault.UnitQuarter: "每季度"}
	fmt.Fprintf(c.out, "已把 %s 的周期设为%s（时区 %s），从下一个周期开始生效。\n", pos[1], names[pos[2]], *tz)
	return nil
}

// localZone is this machine's IANA zone, falling back to UTC.
func localZone() string {
	if tz := os.Getenv("TZ"); tz != "" {
		return tz
	}
	if p, err := os.Readlink("/etc/localtime"); err == nil {
		if _, z, ok := strings.Cut(p, "zoneinfo/"); ok {
			return z
		}
	}
	if n := time.Local.String(); n != "Local" {
		return n
	}
	return "UTC"
}

// timer turns the timed seal on, off, or shows it.
func (c *cli) timer(ctx context.Context, args []string) error {
	pos, err := parseArgs(flag.NewFlagSet("timer", flag.ContinueOnError), args, 3)
	if err != nil {
		return err
	}
	var st *vaultrpc.TimerStatus
	switch pos[2] {
	case "status":
		st, err = c.svc.TimerStatus(ctx, pos[0], pos[1])
	case "off":
		st, err = c.svc.TimerSet(ctx, pos[0], vaultrpc.TimerSetParams{InstanceID: pos[1]})
	default:
		st, err = c.svc.TimerSet(ctx, pos[0], vaultrpc.TimerSetParams{InstanceID: pos[1], Enabled: true, Every: pos[2]})
	}
	if err != nil {
		return err
	}
	if !st.Enabled {
		fmt.Fprintf(c.out, "%s 的定时建立逃生舱：关（服务器上没有 Valise 的定时器单元）\n", pos[1])
		return nil
	}
	fmt.Fprintf(c.out, "%s 的定时建立逃生舱：每 %s 一次（轻装），之后按云备份的自动同步设置顺带同步。\n", pos[1], st.Every)
	fmt.Fprintln(c.out, "服务器上写了这两个 systemd 用户单元，关闭时会删除：")
	for _, u := range st.Units {
		fmt.Fprintf(c.out, "  %s\n", u)
	}
	if !st.Linger {
		fmt.Fprintln(c.out, "注意：这台服务器没有为你的用户开启 linger，你没有登录时定时器不会运行。需要时由管理员执行 loginctl enable-linger <用户>。")
	}
	return nil
}

// shell opens an interactive login shell on the server with the system
// OpenSSH (for running a harness's own commands, e.g. to get a token).
func (c *cli) shell(ctx context.Context, args []string) error {
	pos, err := parseArgs(flag.NewFlagSet("shell", flag.ContinueOnError), args, 1)
	if err != nil {
		return err
	}
	cmd, err := c.svc.ShellCommand(ctx, pos[0])
	if err != nil {
		return err
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	fmt.Fprintf(c.out, "正在打开 %s 的终端，输入 exit 返回。Valise 不读取这个终端里的任何内容。\n", pos[0])
	return cmd.Run()
}
