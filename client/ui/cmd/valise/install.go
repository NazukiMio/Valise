package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"valise/core/valise"
	"valise/ui/service"
)

// install: valise install <服务器> --profile <id> [--show-script] [--yes] [--full-access]
// (docs/INSTALL.md section 3).
func (c *cli) install(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	prof := fs.String("profile", "", "")
	show := fs.Bool("show-script", false, "")
	yes := fs.Bool("yes", false, "")
	full := fs.Bool("full-access", false, "")
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
	if len(plan.Missing) > 0 && *full {
		if plan, err = c.fullAccessRequirements(ctx, plan, *yes); err != nil {
			return err
		}
	} else if len(plan.Missing) > 0 {
		hint := "自己在服务器上装好（valise shell " + host + "）或交给服务器管理员，装好后重新运行"
		if len(plan.Unfixable) == 0 && c.svc.FullAccessReady(host) == nil {
			hint += "；也可以加 --full-access 让 Valise 在完全访问模式下代为安装"
		}
		return valise.New("install_requirements", "服务器还缺少方案需要的程序，没有开始安装", hint)
	} else if *full {
		fmt.Fprintln(c.out, "依赖都已满足，不需要开启完全访问模式。")
	}
	if !*yes && !c.confirm("按上面的方案在 "+host+" 上安装？") {
		return valise.New("cancelled", "已取消，服务器上没有任何改动", "")
	}
	fmt.Fprintln(c.out, "接下来每一步都在服务器的交互终端里运行；需要回答问题时直接在这里输入。Valise 不读取、不保存终端里的内容。")
	res, err := c.svc.RunInstall(ctx, plan, service.InstallIO{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr,
		Step: func(i int, st service.PlanStep) {
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
	fmt.Fprintf(c.out, "下一步：\n  纳入管理：valise enroll %s --profile %s --accept\n  打开管理页：valise open %s %s\n", host, plan.ProfileID, host, plan.ProfileID)
	if len(plan.NativeCLI) > 0 {
		fmt.Fprintf(c.out, "  模型和渠道等设置在管理页里填写，或用原生界面：valise native %s --profile %s\n", host, plan.ProfileID)
	}
	return nil
}

func (c *cli) printInstallPlan(plan *service.InstallPlan, show bool) {
	fmt.Fprintf(c.out, "安装方案：%s（%s，profile v%d）\n  %s\n", plan.ProfileID, plan.ID, plan.ProfileVersion, plan.Summary)
	switch {
	case plan.ProfileLocal:
		fmt.Fprintln(c.out, "  ⚠ 这是本地 profile，未经审核。")
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
		fmt.Fprintln(c.out, "  profile 给出的安装命令（需要管理员权限；Valise 只在完全访问模式下代为执行，加 --full-access）：")
		for _, f := range plan.Fixes {
			fmt.Fprintf(c.out, "    %s\n", strings.Join(f, " "))
		}
	}
	fmt.Fprintln(c.out, "\n步骤：")
	for i, st := range plan.Steps {
		fmt.Fprintf(c.out, "  %d. %s\n", i+1, st.Summary)
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
	cmd, done, err := c.svc.NativeCommand(ctx, pos[0], *prof)
	if err != nil {
		return err
	}
	defer done()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	fmt.Fprintf(c.out, "正在打开 %s 在 %s 上的原生界面（独立的管理连接）。Valise 不读取、不保存这里输入的任何内容。\n", *prof, pos[0])
	return cmd.Run()
}
