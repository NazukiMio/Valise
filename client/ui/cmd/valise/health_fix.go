package main

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"valise/client/ssh"
	"valise/core/valise"
	"valise/ui/service"
)

// healthFix: valise health fix <服务器> <实例> <linger|clock>
// [--advanced] [--yes] [--digest d] [--json]
//
// Fixes built into Valise (docs/N2_IMPLEMENTATION_2026-11-05.md addendum E):
// the confirmation lists every command and the system setting it changes;
// the user agrees again before anything runs. --advanced lets the user turn
// advanced mode on here (both passphrases and the consent sentence) for a
// fix that needs sudo. --digest binds the run to a confirmation already
// shown elsewhere (the terminal UI).
func (c *cli) healthFix(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("health fix", flag.ContinueOnError)
	advanced := fs.Bool("advanced", false, "")
	yes := fs.Bool("yes", false, "")
	digest := fs.String("digest", "", "")
	asJSON := fs.Bool("json", false, "")
	pos, err := parseArgs(fs, args, 3)
	if err != nil {
		return err
	}
	host, inst, id := pos[0], pos[1], pos[2]
	opts := service.SystemFixOptions{Sudo: *advanced}
	f, err := c.svc.PlanSystemFix(ctx, host, inst, id, opts)
	if err != nil {
		return err
	}
	if *asJSON {
		return c.printJSON(f)
	}
	c.printSystemFix(f)
	if *digest != "" && *digest != f.Digest {
		return valise.New("system_fix_plan_changed", "服务器状态或执行方式和你确认时不同，修复没有执行", "回到健康页重新发起修复，核对新的命令")
	}
	switch {
	case f.Mode == service.FixManual:
		fmt.Fprintf(c.out, "\nValise 不能代为执行：%s\n请自己登录服务器执行上面的命令（valise shell %s）。\n", f.Why, ssh.Quote(host))
		return nil
	case f.Mode == service.FixAdvanced && !f.Ready && !*advanced:
		fmt.Fprintf(c.out, "\n需要管理员权限。由你开启高级模式后执行：\n  valise health fix %s %s %s --advanced\n或者自己在服务器上执行上面的命令。\n",
			ssh.Quote(host), ssh.Quote(inst), id)
		return nil
	}
	digestNow := f.Digest
	run := func(tio service.InstallIO) error {
		res, err := c.svc.RunSystemFix(ctx, host, inst, id, digestNow, opts, tio)
		if res != nil {
			c.printSystemFixResult(res)
		}
		return err
	}
	if f.Mode == service.FixAdvanced && !f.Ready {
		return c.withAdvanced(ctx, host, "修复命令（Valise 内置，系统设置）", service.SystemFixSteps(f), *yes, run)
	}
	if !*yes && !c.confirm("执行上面的命令？") {
		return valise.New("cancelled", "已取消，没有执行任何命令", "")
	}
	return run(service.InstallIO{Stdin: c.in, Stdout: c.out, Stderr: c.errOut})
}

func (c *cli) printSystemFix(f *service.SystemFix) {
	fmt.Fprintf(c.out, "%s（系统设置）\n会发生什么：%s\n影响：%s\n", f.Name, f.Goal, f.Impact)
	if len(f.Steps) > 0 {
		fmt.Fprintln(c.out, "\n将执行的命令与改变的系统设置：")
		for i, st := range f.Steps {
			fmt.Fprintf(c.out, "  %d. %s\n", i+1, strings.Join(st.Command, " "))
			if st.When != "" {
				fmt.Fprintf(c.out, "     %s\n", st.When)
			}
			fmt.Fprintf(c.out, "     改变：%s\n     撤销：%s\n", st.Change, st.Undo)
		}
	} else {
		fmt.Fprintln(c.out, "\n需要在服务器上执行的命令：")
		for _, cmd := range f.Manual {
			fmt.Fprintf(c.out, "  %s\n", strings.Join(cmd, " "))
		}
	}
	fmt.Fprintf(c.out, "执行方式：%s\n", f.Access)
}

func (c *cli) printSystemFixResult(r *service.SystemFixResult) {
	fmt.Fprintln(c.out)
	for _, st := range r.Steps {
		cmd := strings.Join(st.Command, " ")
		switch {
		case !st.Ran:
			fmt.Fprintf(c.out, "  · 未执行：%s（%s）\n", cmd, st.Note)
		case st.Exit == 0:
			fmt.Fprintf(c.out, "  ✓ 命令：%s · 退出码 0\n", cmd)
		default:
			fmt.Fprintf(c.out, "  ✗ 命令：%s · 退出码 %d\n", cmd, st.Exit)
		}
	}
	if v := r.Verification; v != nil {
		mk := map[string]string{"resolved": "✓", "remaining": "✗"}[v.State]
		if mk == "" {
			mk = "·"
		}
		fmt.Fprintf(c.out, "  %s 该问题复核结果：%s\n", mk, v.Text)
	}
	if r.Next != "" {
		fmt.Fprintf(c.out, "下一步：%s\n", r.Next)
	}
	if !r.Audited {
		fmt.Fprintln(c.out, "注意：审计记录没有写入服务器（连接问题）；结果以上面为准。")
	}
}
