package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"valise/core/install"
	"valise/core/valise"
	"valise/ui/service"
)

// line reads one visible line from stdin.
func (c *cli) line(prompt string) string {
	fmt.Fprint(c.errOut, prompt)
	if c.lines == nil {
		c.lines = bufio.NewReader(c.in)
	}
	s, _ := c.lines.ReadString('\n')
	return strings.TrimSpace(s)
}

// fullAccessRequirements enables full access mode for the plan's server
// and installs the missing requirements (docs/INSTALL.md section 4.1). It
// returns the plan checked again afterwards.
func (c *cli) fullAccessRequirements(ctx context.Context, plan *service.InstallPlan, yes bool) (*service.InstallPlan, error) {
	host := plan.Host
	if err := c.svc.FullAccessReady(host); err != nil {
		return nil, err
	}
	batch := service.CheckRequirements(plan)
	if len(batch.Unfixable) > 0 {
		return nil, valise.New("install_requirements_unfixable", "profile 没有为这台服务器的发行版给出 "+batch.Unfixable[0].Binary+" 的安装命令",
			"自己在服务器上装好它（valise shell "+host+"），或交给服务器管理员")
	}
	fmt.Fprintln(c.out, "\n要执行的依赖安装命令（来源：profile）：")
	for i, cmd := range batch.Commands {
		fmt.Fprintf(c.out, "  %d. %s\n", i+1, strings.Join(cmd.Command, " "))
		if len(cmd.Findings) == 0 {
			fmt.Fprintf(c.out, "     %s\n", install.NoFindings)
		}
		for _, f := range cmd.Findings {
			mark := "疑点"
			if f.Refuse {
				mark = "一律不执行"
			}
			fmt.Fprintf(c.out, "     %s：%s\n", mark, f.Reason)
		}
	}
	if batch.Refused {
		return nil, valise.New("full_access_refused", "这批命令里有 Valise 一律不执行的命令，整批都没有执行",
			"Valise 不会代为执行它们，也没有绕过的开关；如果坚持，只能自己登录服务器执行")
	}
	fmt.Fprintf(c.out, "\n开启完全访问模式（%s）\n", host)
	for _, r := range service.FullAccessRisks {
		fmt.Fprintf(c.out, "  ⚠ %s\n", r)
	}
	fmt.Fprintf(c.out, "完全访问模式只在这次运行中有效；管理钥匙的时间窗口（%d 分钟）到期后，下一步之前会再次要求管理口令。\n",
		int(c.svc.AdminWindow().Minutes()))
	vp, err := c.passphrase("Valise 口令：")
	if err != nil {
		return nil, err
	}
	ap, err := c.passphrase(host + " 的管理口令：")
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(c.out, "请输入确认句：%s\n", service.FullAccessConsent)
	consent := c.line("> ")
	if err := c.svc.EnableFullAccess(ctx, host, vp, ap, consent); err != nil {
		return nil, err
	}
	defer c.svc.DisableFullAccess(ctx)
	fmt.Fprintf(c.out, "完全访问模式已开启 · %s\n", host)
	fmt.Fprintln(c.out, "需要 sudo 密码时，直接在下面的服务器终端里输入；Valise 不读取、不保存。")
	if batch.Sentence {
		fmt.Fprintf(c.out, "上面有疑点。请输入确认句：%s\n", install.RiskSentence)
		if c.line("> ") != install.RiskSentence {
			return nil, valise.New("cancelled", "确认句不一致，没有执行任何命令", "")
		}
	}
	if !yes && !c.confirm("执行上面这批命令？") {
		return nil, valise.New("cancelled", "已取消，没有执行任何命令", "")
	}
	again, err := c.svc.RunRequirements(ctx, plan, batch, service.InstallIO{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr,
		Step: func(i int, st service.PlanStep) {
			if fa := c.svc.FullAccess(ctx); fa != nil {
				fmt.Fprintf(c.out, "\n[Valise · 完全访问模式 · %s] 第 %d/%d 条：%s\n", fa.Host, i+1, len(batch.Commands), st.Summary)
			}
		}})
	if err != nil {
		return nil, err
	}
	fmt.Fprintln(c.out, "\n依赖已全部就绪：")
	for _, q := range again.Check.Requirement {
		fmt.Fprintf(c.out, "  ✓ %s %s\n", q.Binary, q.Version)
	}
	return again, nil
}
