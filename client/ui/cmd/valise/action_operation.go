package main

import (
	"context"
	"flag"
	"fmt"

	"valise/core/action"
	"valise/core/valise"
	"valise/ui/service"
)

func (c *cli) actionOperation(ctx context.Context, args []string) error {
	verb := args[0]
	fs := flag.NewFlagSet("action "+verb, flag.ContinueOnError)
	yes := fs.Bool("yes", false, "")
	cont := fs.Bool("continue", false, "")
	restore := fs.Bool("restore-services", false, "")
	pos, err := parseArgs(fs, args[1:], 2)
	if err != nil {
		return err
	}
	host, op := pos[0], pos[1]
	j, err := c.svc.ActionStatus(ctx, host, op)
	if err != nil {
		return err
	}
	if verb == "status" {
		return c.printActionOperation(j, 0)
	}
	if verb == "cleanup" {
		paths, err := c.svc.CleanupAction(ctx, host, op, false)
		if err != nil {
			return err
		}
		if len(paths) == 0 {
			fmt.Fprintln(c.out, "没有待清理目录。")
			return nil
		}
		fmt.Fprintln(c.out, "将删除以下暂存或旧目录；保留活动目录、备份和操作记录：")
		for _, p := range paths {
			fmt.Fprintln(c.out, "  "+p)
		}
		if !*yes && !c.confirm("清理这些目录？") {
			return valise.New("cancelled", "已取消清理", "")
		}
		_, err = c.svc.CleanupAction(ctx, host, op, true)
		return err
	}
	if *cont == *restore {
		return valise.New("action_resume_choice", "必须选择 --continue 或 --restore-services 其中一种", "结果不明的配置步骤只能放弃暂存修改并恢复原服务状态")
	}
	choice := action.ResumeRestore
	if *cont {
		choice = action.ResumeContinue
		if !j.Published && !service.CanContinueAction(j) {
			return valise.New("action_resume_unknown", "配置步骤未全部完成并复核，不能继续发布", "使用 --restore-services 放弃尚未发布的修改并恢复原服务状态")
		}
	}
	for _, line := range service.ActionOperationLines(j) {
		fmt.Fprintln(c.out, "  "+line)
	}
	explanation := "放弃尚未发布的暂存修改并恢复原服务状态"
	if j.Published {
		explanation = "保留已发布配置并恢复原服务状态"
	} else if *cont {
		explanation = "发布已复核的暂存配置并恢复原服务状态"
	}
	fmt.Fprintln(c.out, explanation+"；不会重跑结果不明的步骤。")
	if err = c.gate(ctx, host, j.Plan.InstanceID, *yes); err != nil {
		return err
	}
	if !*yes && !c.confirm(explanation+"？") {
		return valise.New("cancelled", "已取消，保留当前操作状态", "")
	}
	res, err := c.svc.ResumeAction(ctx, host, op, choice)
	if err != nil {
		return err
	}
	return c.printActionOperation(res.Operation, res.Exit)
}
func (c *cli) printActionOperation(j *action.Operation, exit int) error {
	fmt.Fprintln(c.out, "\n"+j.Plan.Definition.Name)
	for _, line := range service.ActionOperationLines(j) {
		fmt.Fprintln(c.out, "  "+line)
	}
	fmt.Fprintln(c.out, "  操作："+j.ID)
	if !j.Done {
		fmt.Fprintln(c.out, "  操作尚未结束：用 valise action resume <服务器> "+j.ID+" --restore-services 恢复原服务状态。")
	}
	if exit != 0 {
		return valise.New("action_incomplete", "配置事务未全部完成", "分别查看配置、审批检查与服务状态；未完成操作可按上方命令恢复")
	}
	return nil
}
