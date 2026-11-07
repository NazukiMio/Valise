package main

import (
	"bufio"
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
	"valise/core/vault"
	"valise/core/vaultrpc"
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
	c.printInstallSummary(plan)
	if *show {
		c.printInstallPlan(plan, true)
	}
	if err := c.existingAgent(ctx, plan, *yes); err != nil {
		return err
	}
	if !*yes {
		if err := c.leftovers(ctx, host); err != nil {
			return err
		}
	}
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
	if !*yes && !c.confirmOrDetails("按上面的方案在 "+host+" 上安装？", plan) {
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
	for _, n := range plan.Notes {
		fmt.Fprintln(c.out, "  "+n)
	}
	// Installed by Valise: offer to connect it right away (docs/UI.md 9.6).
	if !*yes {
		ask := "现在接入 Valise？接入只读取和记录，不改动 agent 的任何文件。先显示接入报告"
		if plan.Existing != nil && plan.Existing.Enrolled != "" {
			ask = "它之前已经接入 Valise。现在重新检测，把新版本记进实例档案？先显示与档案的差异"
		}
		fmt.Fprintln(c.out)
		if c.confirm(ask) {
			if err := c.enroll(ctx, []string{host, "--profile", plan.ProfileID}); err != nil {
				return err
			}
			if c.confirm("接受上面的接入报告，把它接入 Valise？") {
				return c.enroll(ctx, []string{host, "--profile", plan.ProfileID, "--accept"})
			}
		}
	}
	fmt.Fprintf(c.out, "下一步：\n  接入 Valise：valise enroll %s --profile %s --accept\n  打开管理页：valise open %s %s\n", host, plan.ProfileID, host, plan.ProfileID)
	if len(plan.NativeCLI) > 0 {
		fmt.Fprintf(c.out, "  模型和渠道等设置在管理页里填写，或用原生界面：valise native %s --profile %s\n", host, plan.ProfileID)
	}
	return nil
}

// printInstallSummary is the plan in four lines (docs/UI.md 9.6); the
// commands, scripts and hashes are the details.
func (c *cli) printInstallSummary(plan *service.InstallPlan) {
	fmt.Fprintf(c.out, "安装 %s 到 %s\n\n", plan.ProfileID, plan.Host)
	row := func(k, v string) {
		if v != "" {
			fmt.Fprintf(c.out, "  %-6s  %s\n", k, v)
		}
	}
	row("装什么", plan.Summary)
	row("装到哪里", plan.Where)
	row("大概多久", plan.Takes)
	need := "只用你的用户权限，不需要管理员；敏感操作，要用服务器密码或管理钥匙在服务器上开一个终端来执行"
	if len(plan.Missing) > 0 {
		var names []string
		for _, q := range plan.Missing {
			names = append(names, q.Binary)
		}
		need = "服务器还缺 " + strings.Join(names, "、") + "，要先用管理员权限装上（见下）"
	}
	row("需要什么", need)
	switch {
	case plan.ProfileLocal:
		fmt.Fprintln(c.out, "\n  ⚠ 这是本地 harness 资料，未经审核。")
	case plan.Capability != "tested":
		fmt.Fprintln(c.out, "\n  ⚠ 这个安装方案在当前版本上尚未完整测试（"+plan.Capability+"）。")
	}
	if len(plan.Missing) > 0 {
		fmt.Fprintln(c.out)
		for _, q := range plan.Check.Requirement {
			if !q.OK {
				fmt.Fprintf(c.out, "  ✗ %s %s\n", q.Binary, q.Reason)
			}
		}
		for _, f := range plan.Fixes {
			fmt.Fprintf(c.out, "    安装命令（Valise 只在高级模式下代为执行，加 --advanced）：%s\n", strings.Join(f, " "))
		}
	}
	if len(plan.Notes) > 0 {
		fmt.Fprintln(c.out)
		for _, n := range plan.Notes {
			fmt.Fprintln(c.out, "  "+n)
		}
	}
	fmt.Fprintln(c.out)
}

// confirmOrDetails asks to go on; v shows the commands, scripts and hashes
// first.
func (c *cli) confirmOrDetails(prompt string, plan *service.InstallPlan) bool {
	for {
		fmt.Fprint(c.errOut, prompt+" [y/N，v 查看实际命令、脚本与哈希] ")
		if c.lines == nil {
			c.lines = bufio.NewReader(c.in)
		}
		line, _ := c.lines.ReadString('\n')
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "y", "yes", "是":
			return true
		case "v":
			c.printInstallPlan(plan, false)
			fmt.Fprintln(c.out)
			continue
		}
		return false
	}
}

// existingAgent stops when the harness already has data on the server:
// the install would take it over (docs/UI.md 9.6).
func (c *cli) existingAgent(ctx context.Context, plan *service.InstallPlan, yes bool) error {
	ex := plan.Existing
	if ex == nil {
		return nil
	}
	v := ex.Version
	if v == "" {
		v = "版本未知"
	}
	fmt.Fprintf(c.out, "⚠ 服务器上已经有 %s 的数据：%s（%s）。直接安装，新装的程序会接管这份数据。\n", plan.ProfileID, ex.StateDir, v)
	if ex.Enrolled != "" {
		fmt.Fprintf(c.out, "  它已经接入 Valise（实例 %s）。\n", ex.Enrolled)
	}
	if yes {
		return valise.New("install_existing", "服务器上已经有 "+plan.ProfileID+" 的数据，没有开始安装",
			"不带 --yes 运行，选择先给旧数据建立备份，或确认让新程序接管")
	}
	fmt.Fprintln(c.out, "\n  1  先给旧数据建一个备份（标准档位），再安装")
	fmt.Fprintln(c.out, "  2  直接安装，新程序接管这份数据")
	fmt.Fprintln(c.out, "  3  取消")
	fmt.Fprint(c.errOut, "选择 [1/2/3] ")
	if c.lines == nil {
		c.lines = bufio.NewReader(c.in)
	}
	line, _ := c.lines.ReadString('\n')
	switch strings.TrimSpace(line) {
	case "1":
		inst := ex.Enrolled
		if inst == "" {
			if err := c.enroll(ctx, []string{plan.Host, "--profile", plan.ProfileID}); err != nil {
				return err
			}
			if !c.confirm("先把旧数据接入 Valise（只读取和记录），然后建立备份？") {
				return valise.New("cancelled", "已取消，服务器上没有任何改动", "")
			}
			if err := c.enroll(ctx, []string{plan.Host, "--profile", plan.ProfileID, "--accept"}); err != nil {
				return err
			}
			inst = ex.ID
			ex.Enrolled = inst
		}
		fmt.Fprintln(c.out, "正在建立备份…")
		rep, err := c.svc.Seal(c.withProgress(ctx), plan.Host, vaultrpc.SealParams{InstanceID: inst, Tier: vault.TierSoulSecrets, Reason: "安装前"})
		if err != nil {
			return err
		}
		fmt.Fprintf(c.out, "备份已建立：%d 个文件，%s。\n\n", rep.Files, size(rep.PlainBytes))
		return nil
	case "2":
		return nil
	}
	return valise.New("cancelled", "已取消，服务器上没有任何改动", "")
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
	if err := c.leftovers(ctx, pos[0]); err != nil {
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

// leftovers shows the terminal programs Valise started on host that are
// still running, typically after a dropped connection (review F05), and
// lets the user end them, leave them, or stop. Nothing is ended without
// the user's yes, and porter ends only programs it started itself.
func (c *cli) leftovers(ctx context.Context, host string) error {
	list, err := c.svc.TerminalSessions(ctx, host)
	if err != nil || len(list) == 0 {
		return nil // nothing known, or an older porter: go on
	}
	fmt.Fprintf(c.out, "%s 上还有 Valise 之前打开的程序在运行：\n", host)
	for _, s := range list {
		state := "连接还在"
		if s.Detached {
			state = "当时的连接已经断开"
		}
		fmt.Fprintf(c.out, "  · %s（PID %d，%s开始，%s）\n", strings.Join(s.Argv, " "), s.PID, s.StartedAt.Local().Format("01-02 15:04"), state)
	}
	fmt.Fprintln(c.out, "它可能还占着配置，新打开的同一个程序会因此报错。")
	fmt.Fprintln(c.out, "  1  结束它们，再继续（需要开发者模式）")
	fmt.Fprintln(c.out, "  2  不管它们，直接继续")
	fmt.Fprintln(c.out, "  3  取消")
	fmt.Fprint(c.errOut, "选择 [1/2/3] ")
	if c.lines == nil {
		c.lines = bufio.NewReader(c.in)
	}
	line, _ := c.lines.ReadString('\n')
	switch strings.TrimSpace(line) {
	case "1":
		for _, s := range list {
			if err := c.svc.EndTerminalSession(ctx, host, s.PID); err != nil {
				return err
			}
			fmt.Fprintf(c.out, "  已结束 PID %d\n", s.PID)
		}
		return nil
	case "2":
		return nil
	}
	return valise.New("cancelled", "已取消，没有任何改动", "")
}
