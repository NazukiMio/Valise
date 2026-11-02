package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"valise/core/profile"
	"valise/core/valise"
	"valise/ui/service"
)

// action: valise action <服务器> <实例> [<动作>] [--yes] [--no-seal]
// (docs/INSTALL.md section 5).
func (c *cli) action(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("action", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "")
	noSeal := fs.Bool("no-seal", false, "")
	pos, err := parseArgsRange(fs, args, 2, 3)
	if err != nil {
		return err
	}
	host, instance := pos[0], pos[1]
	list, err := c.svc.Actions(ctx, host, instance)
	if err != nil {
		return err
	}
	if len(pos) == 2 {
		return c.printActions(host, list)
	}
	var a *profile.Action
	for i := range list.Actions {
		if list.Actions[i].ID == pos[2] {
			a = &list.Actions[i]
		}
	}
	if a == nil {
		return valise.New("action_unknown", list.ProfileID+" 的 harness 资料里没有动作 "+pos[2], "不带动作名运行，查看可用的动作")
	}
	fmt.Fprintf(c.out, "%s（%s · %s）\n  %s\n", a.Name, service.RiskLabels[a.Risk], service.ActionAccess(*a), a.Explain)
	if a.Undo != "" {
		fmt.Fprintf(c.out, "  能否撤销：%s\n", a.Undo)
	}
	fmt.Fprintf(c.out, "  实际命令：%s\n", strings.Join(a.Argv, " "))
	seal := service.SealsByDefault(*a) && !*noSeal
	if service.SealsByDefault(*a) {
		if seal {
			fmt.Fprintln(c.out, "  执行前先建立一个逃生舱；出了问题可以从它原地着陆。")
		} else {
			fmt.Fprintln(c.out, "  ⚠ 已选择不建逃生舱：出了问题时没有改动之前的状态可以着陆。")
		}
	}
	if a.Risk == profile.RiskConfig || a.Risk == profile.RiskDanger {
		if err := c.gate(ctx, host, instance, *yes); err != nil {
			return err
		}
	}
	if a.Risk != profile.RiskRead && !*yes && !c.confirm("执行「"+a.Name+"」？") {
		return valise.New("cancelled", "已取消，服务器上没有任何改动", "")
	}
	if a.Risk == profile.RiskDanger {
		fmt.Fprintf(c.out, "这是高风险动作。请输入动作名称「%s」确认：\n", a.Name)
		if c.line("> ") != a.Name {
			return valise.New("cancelled", "名称不一致，已取消，服务器上没有任何改动", "")
		}
	}
	res, err := c.svc.RunAction(ctx, host, instance, a.ID, service.ActionOptions{Seal: seal,
		IO:       service.InstallIO{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr},
		Progress: func(m string) { fmt.Fprintf(c.out, "[Valise] %s\n", m) }})
	if err != nil {
		return err
	}
	return c.printActionResult(ctx, a, res)
}

func (c *cli) printActions(host string, list *service.ActionList) error {
	if len(list.Actions) == 0 {
		fmt.Fprintf(c.out, "%s 的 harness 资料没有声明管理动作。\n", list.ProfileID)
		return nil
	}
	fmt.Fprintf(c.out, "%s 的管理动作（来自 %s 的 harness 资料）：\n", list.Instance, list.ProfileID)
	for _, risk := range []string{profile.RiskRead, profile.RiskManage, profile.RiskConfig, profile.RiskDanger} {
		first := true
		for _, a := range list.Actions {
			if a.Risk != risk {
				continue
			}
			if first {
				fmt.Fprintf(c.out, "\n  %s\n", service.RiskLabels[risk])
				first = false
			}
			fmt.Fprintf(c.out, "    %-14s %s · %s\n", a.ID, a.Name, service.ActionAccess(a))
			fmt.Fprintf(c.out, "    %-14s %s\n", "", a.Explain)
		}
	}
	if list.Stale {
		fmt.Fprintf(c.out, "\n注意：服务器上保存的 harness 资料与本机的版本不同，新增的动作要重新接入后才能用（valise enroll %s --profile %s --accept）。\n", host, list.ProfileID)
	}
	fmt.Fprintf(c.out, "\n执行：valise action %s %s <动作>\n", host, list.Instance)
	return nil
}

func (c *cli) printActionResult(ctx context.Context, a *profile.Action, res *service.ActionResult) error {
	fmt.Fprintln(c.out)
	if res.Snapshot != "" {
		fmt.Fprintf(c.out, "  ✓ 逃生舱：%s\n", res.Snapshot)
	}
	mark := "✓"
	if res.Exit != 0 {
		mark = "✗"
	}
	fmt.Fprintf(c.out, "  %s 命令：退出码 %d\n", mark, res.Exit)
	if res.Checked {
		for _, sv := range res.Service {
			m, st := "✓", "运行中"
			if sv.Active != "active" {
				m, st = "✗", sv.Active
			}
			fmt.Fprintf(c.out, "  %s 服务：%s %s\n", m, sv.Unit, st)
		}
		for _, e := range res.Entries {
			m, st := "✓", "在监听"
			if !e.Listening {
				m, st = "✗", "没有监听"
			}
			fmt.Fprintf(c.out, "  %s 管理页端口 %d：%s\n", m, e.Port, st)
		}
	}
	if a.Output == profile.OutputText && res.Output != "" {
		note := ""
		if res.Truncated {
			note = "（只保留最后 200 行）"
		}
		fmt.Fprintf(c.out, "\n命令输出%s：\n%s\n", note, res.Output)
	}
	if a.Output == profile.OutputSecret && res.Exit == 0 {
		return c.holdSecret(ctx, res.Secret)
	}
	if res.Exit != 0 {
		return valise.New("action_failed", fmt.Sprintf("动作「%s」失败（退出码 %d）", a.Name, res.Exit), "看上面的输出与状态；需要时打开原生界面或服务器终端检查")
	}
	return nil
}

// holdSecret copies the secret and keeps the process alive until the
// clipboard is cleared (Enter clears it early).
func (c *cli) holdSecret(ctx context.Context, secret string) error {
	clip, err := service.CopySecret(ctx, secret)
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "  ✓ 已复制到剪贴板（没有显示，也没有保存）。%d 秒后清除；按回车立即清除。\n", int(service.SecretWindow.Seconds()))
	fmt.Fprintln(c.out, "  注意：剪贴板管理工具可能会保留历史记录。")
	enter := make(chan struct{})
	go func() { c.line(""); close(enter) }()
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	start := time.Now()
	for {
		select {
		case <-enter:
			clip.Clear()
			fmt.Fprintln(c.out, "  剪贴板已清除。")
			return nil
		case <-clip.Done():
			fmt.Fprintln(c.out, "  剪贴板已清除。")
			return nil
		case <-tick.C:
			if left := service.SecretWindow - time.Since(start); left > 0 {
				fmt.Fprintf(c.out, "  还剩 %d 秒…\n", int(left.Seconds()+0.5))
			}
		}
	}
}
