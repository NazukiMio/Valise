package main

import (
	"context"
	"flag"
	"fmt"

	"valise/ui/service"
)

// health: valise health <服务器> <实例> [--json]
func (c *cli) health(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("health", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "")
	pos, err := parseArgs(fs, args, 2)
	if err != nil {
		return err
	}
	r, err := c.svc.Health(ctx, pos[0], pos[1])
	if err != nil {
		return err
	}
	if *asJSON {
		return c.printJSON(r)
	}
	if n := r.Incomplete(); n > 0 {
		fmt.Fprintf(c.out, "! 部分检查未完成（%d 项），暂无法完整判断；已知 %d 件事要留意\n", n, r.Problems())
	} else if n := r.Problems(); n == 0 {
		fmt.Fprintln(c.out, "✓ 已完成检查未发现问题")
	} else {
		fmt.Fprintf(c.out, "! %d 件事要留意\n", n)
	}
	for _, check := range r.Checks {
		if check.State != "ok" {
			fmt.Fprintf(c.out, "  · %s：%s\n", check.Title, check.Reason)
		}
	}
	group := ""
	for _, it := range r.Items {
		if it.Group != group {
			group = it.Group
			if group == service.HealthPods {
				fmt.Fprintln(c.out, "\n逃生舱状态")
			} else {
				fmt.Fprintln(c.out, "\nagent 健康")
			}
		}
		mark := map[string]string{service.HealthOK: "✓", service.HealthWarn: "!", service.HealthBad: "✗"}[it.Level]
		if mark == "" {
			mark = "·"
		}
		fmt.Fprintf(c.out, "  %s %s：%s\n", mark, it.Title, it.Text)
		if it.Detail != "" && it.Level != service.HealthOK {
			fmt.Fprintf(c.out, "      %s\n", it.Detail)
		}
	}
	return nil
}

// restoreTest: valise restore-test <服务器> <实例> [--snapshot id]
func (c *cli) restoreTest(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("restore-test", flag.ContinueOnError)
	snap := fs.String("snapshot", "", "")
	pos, err := parseArgs(fs, args, 2)
	if err != nil {
		return err
	}
	fmt.Fprintln(c.out, "在这台电脑和服务器上各解开一次逃生舱并逐个核对文件；不切换、不碰 agent，结束后删除临时文件。")
	r, err := c.svc.RunRestoreTest(c.withProgress(ctx), pos[0], pos[1], *snap)
	if err != nil {
		return err
	}
	if !r.OK {
		where := map[string]string{service.TestLocal: "这台电脑上", service.TestServer: "服务器上"}[r.Step]
		return fmt.Errorf("恢复测试没有通过（%s）：%s", where, r.Problem)
	}
	fmt.Fprintf(c.out, "✓ 恢复测试通过：%s 的逃生舱（%s），两边都解开并核对了 %d 个文件，没有切换，临时文件已删除。\n",
		r.Created.Local().Format("01-02 15:04"), r.Tier, r.Files)
	if r.Downtime != "" {
		fmt.Fprintf(c.out, "  真正恢复时还要：%s\n", r.Downtime)
	}
	if l := transferLine(r.Transfer); l != "" {
		fmt.Fprintln(c.out, "  "+l)
	}
	return nil
}
