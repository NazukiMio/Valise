package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"valise/client/ssh"
	"valise/core/valise"
	"valise/ui/service"
)

// install: valise install <服务器> --profile <id> [--show-script] [--yes] [--advanced]
// (docs/INSTALL.md section 3).
func (c *cli) install(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	prof := fs.String("profile", "", "")
	show := fs.Bool("show-script", false, "")
	yes := fs.Bool("yes", false, "")
	advanced := fs.Bool("advanced", false, "")
	var with multiFlag
	fs.Var(&with, "with", "")
	pos, err := parseArgs(fs, args, 1)
	if err != nil || *prof == "" {
		return errUsage
	}
	host := pos[0]
	plan, err := c.svc.PlanInstall(ctx, host, *prof)
	if err != nil {
		return err
	}
	c.printInstallPlan(plan, *show)
	if err := c.gate(ctx, host, "", *yes); err != nil {
		return err
	}
	if len(plan.Missing) > 0 && *advanced {
		if plan, err = c.advancedRequirements(ctx, plan, *yes); err != nil {
			return err
		}
	} else if len(plan.Missing) > 0 {
		hint := "自己在服务器上装好（valise shell " + host + "）或交给服务器管理员，装好后重新运行"
		if len(plan.Unfixable) == 0 && c.svc.AdvancedReady(host) == nil {
			hint += "；也可以加 --advanced 让 Valise 在高级模式下代为安装"
		}
		return valise.New("install_requirements", "服务器还缺少方案需要的程序，没有开始安装", hint)
	} else if *advanced {
		fmt.Fprintln(c.out, "依赖都已满足，不需要开启高级模式。")
	}
	if !*yes && !c.confirm("按上面的方案在 "+host+" 上安装？") {
		return valise.New("cancelled", "已取消，服务器上没有任何改动", "")
	}
	if err := c.chooseOptional(plan, with, *yes); err != nil {
		return err
	}
	fmt.Fprintln(c.out, "接下来每一步都在服务器的交互终端里运行；需要回答问题时直接在这里输入。Valise 不读取、不保存终端里的内容。")
	res, err := c.svc.RunInstall(ctx, plan, service.InstallIO{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr,
		Step: func(i int, st service.PlanStep) {
			if st.Optional && !st.Selected {
				fmt.Fprintf(c.out, "\n[Valise] 第 %d/%d 步：跳过可选步骤「%s」\n", i+1, len(plan.Steps), st.Summary)
				return
			}
			fmt.Fprintf(c.out, "\n[Valise] 第 %d/%d 步：%s\n", i+1, len(plan.Steps), st.Summary)
		}})
	if err != nil {
		return err
	}
	fmt.Fprintln(c.out)
	if res.Found == nil {
		fmt.Fprintln(c.out, "所有步骤都已执行，但 Valise 没有在服务器上找到装好的实例。")
		if len(plan.NativeCLI) > 0 {
			fmt.Fprintf(c.out, "可以用原生方式继续：valise native %s --profile %s\n", host, plan.ProfileID)
		}
		return valise.New("install_not_found", "安装后没有检测到实例", "看上面的输出，或用原生方式按官方文档继续")
	}
	v := res.Found.Version
	if v == "" {
		v = "版本未知"
	}
	fmt.Fprintf(c.out, "安装完成：在 %s 找到 %s（%s）。\n", res.Found.StateDir, plan.ProfileID, v)
	fmt.Fprintf(c.out, "下一步：\n  接入 Valise：valise enroll %s --profile %s --accept\n  打开管理页：valise open %s %s\n", host, plan.ProfileID, host, plan.ProfileID)
	if len(plan.NativeCLI) > 0 {
		fmt.Fprintf(c.out, "  模型和渠道等设置在管理页里填写，或用原生界面：valise native %s --profile %s\n", host, plan.ProfileID)
	}
	return nil
}

func (c *cli) printInstallPlan(plan *service.InstallPlan, show bool) {
	fmt.Fprintf(c.out, "安装方案：%s（%s，harness 资料 v%d）\n  %s\n", plan.ProfileID, plan.ID, plan.ProfileVersion, plan.Summary)
	switch {
	case plan.ProfileLocal:
		fmt.Fprintln(c.out, "  ⚠ 这是本地 harness 资料，未经审核。")
	case plan.Capability != "tested":
		fmt.Fprintln(c.out, "  ⚠ 这个安装方案在当前版本上尚未完整测试（"+plan.Capability+"）。")
	}
	fmt.Fprintf(c.out, "\n服务器：%s %s，可用空间 %s\n", plan.Check.OSID, plan.Check.OSVersion, size(int64(plan.Check.FreeBytes)))
	for _, q := range plan.Check.Requirement {
		mark, detail := "✓", q.Version
		if !q.OK {
			mark, detail = "✗", q.Reason
		}
		fmt.Fprintf(c.out, "  %s %s %s\n", mark, q.Binary, detail)
	}
	if len(plan.Fixes) > 0 {
		fmt.Fprintln(c.out, "  harness 资料给出的安装命令（需要管理员权限；Valise 只在高级模式下代为执行，加 --advanced）：")
		for _, f := range plan.Fixes {
			fmt.Fprintf(c.out, "    %s\n", strings.Join(f, " "))
		}
	}
	fmt.Fprintln(c.out, "\n步骤：")
	for i, st := range plan.Steps {
		fmt.Fprintf(c.out, "  %d. %s\n", i+1, st.Summary)
		if st.Optional {
			fmt.Fprintf(c.out, "     可选步骤（%s），默认不执行。不执行的话：%s\n", st.ID, st.SkipEffect)
		}
		if st.Note != "" {
			fmt.Fprintf(c.out, "     %s\n", st.Note)
		}
		fmt.Fprintf(c.out, "     实际命令：%s\n", strings.Join(st.Command, " "))
		if st.URL == "" {
			continue
		}
		fmt.Fprintf(c.out, "     来源：%s\n     SHA-256：%s\n", st.URL, st.SHA256)
		if st.RuntimeDownloads != "" {
			fmt.Fprintf(c.out, "     ⚠ 运行时还会下载：%s（Valise 无法预先检查这些内容）\n", st.RuntimeDownloads)
		}
		if p, err := c.saveScript(plan.ID, st); err == nil {
			fmt.Fprintf(c.out, "     脚本全文已存到本机：%s\n", p)
		}
		if show {
			fmt.Fprintf(c.out, "----- %s -----\n%s\n----- 脚本结束 -----\n", path.Base(st.URL), st.Script)
		}
	}
}

// saveScript keeps a copy of a verified script for the user to read.
func (c *cli) saveScript(planID string, st service.PlanStep) (string, error) {
	dir := filepath.Join(c.svc.Dirs.Cache, "install", planID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	p := filepath.Join(dir, path.Base(st.URL))
	return p, os.WriteFile(p, st.Script, 0o600)
}

// native: valise native <服务器> --profile <id>
func (c *cli) native(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("native", flag.ContinueOnError)
	prof := fs.String("profile", "", "")
	pos, err := parseArgs(fs, args, 1)
	if err != nil || *prof == "" {
		return errUsage
	}
	if err := c.gate(ctx, pos[0], "", false); err != nil {
		return err
	}
	cmd, done, err := c.svc.NativeCommand(ctx, pos[0], *prof)
	if err != nil {
		return err
	}
	defer done()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	fmt.Fprintf(c.out, "正在打开 %s 在 %s 上的原生界面（独立的管理连接）。Valise 不读取、不保存这里输入的任何内容。\n", *prof, pos[0])
	if hints := c.svc.NativeHints(*prof); len(hints) > 0 {
		fmt.Fprintln(c.out, "提示：")
		for _, h := range hints {
			fmt.Fprintf(c.out, "  · %s\n", strings.NewReplacer("<服务器>", pos[0], "<实例>", *prof).Replace(h))
		}
		fmt.Fprintf(c.out, "  · 需要另开终端运行 %s 自己的命令时：valise shell %s --profile %s\n", *prof, pos[0], *prof)
	}
	return c.interactiveEnd(cmd.Run(), "原生界面")
}

// interactiveEnd reports how a terminal session on the server ended. The
// program's own exit status (Ctrl+C included) is the user's business, not
// a Valise failure; only a lost connection is an error.
func (c *cli) interactiveEnd(err error, what string) error {
	var ee *exec.ExitError
	if err == nil || !errors.As(err, &ee) {
		return err
	}
	switch code := ee.ExitCode(); code {
	case ssh.ExitConnFailed:
		return valise.New("terminal_conn_failed", "和服务器的连接断开了，"+what+"没有正常结束", "检查网络后重新打开；服务器上已保存的内容不受影响")
	case 130, -1:
		fmt.Fprintf(c.out, "已离开%s（用 Ctrl+C 结束）。已经保存的配置不受影响。\n", what)
	default:
		fmt.Fprintf(c.out, "%s已结束（退出码 %d）。\n", what, code)
	}
	return nil
}

// chooseOptional selects optional steps: --with <id>, or a y/N question per
// step. --yes never selects one (docs/INSTALL.md section 3).
func (c *cli) chooseOptional(plan *service.InstallPlan, with multiFlag, yes bool) error {
	known := map[string]bool{}
	for i := range plan.Steps {
		st := &plan.Steps[i]
		if !st.Optional {
			continue
		}
		known[st.ID] = true
		for _, w := range with {
			if w == st.ID {
				st.Selected = true
			}
		}
		if !st.Selected && !yes {
			fmt.Fprintf(c.out, "\n可选步骤：%s\n  不执行的话：%s\n", st.Summary, st.SkipEffect)
			st.Selected = c.confirm("执行这个可选步骤？")
		}
	}
	for _, w := range with {
		if !known[w] {
			return valise.New("install_optional_unknown", "方案里没有可选步骤 "+w, "去掉这个 --with，或看方案里列出的可选步骤 id")
		}
	}
	return nil
}
