package main

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"valise/core/agentkit"
)

var findingNames = map[string]string{
	agentkit.FindingLongSession:       "会话过长",
	agentkit.FindingCostGrowth:        "token 用量持续上升",
	agentkit.FindingMemoryBloat:       "记忆文件过大",
	agentkit.FindingSessionPileup:     "旧会话堆积",
	agentkit.FindingNoMemoryMechanism: "没有记忆机制",
}

func (c *cli) checkup(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("checkup", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "")
	pos, err := parseArgs(fs, args, 2)
	if err != nil {
		return err
	}
	r, err := c.svc.Checkup(ctx, pos[0], pos[1])
	if err != nil {
		return err
	}
	if *asJSON {
		return c.printJSON(r)
	}
	w := c.out
	fmt.Fprintf(w, "体检：%s（只读，由代码计算）\n", r.InstanceID)
	if r.SessionsDeclared {
		fmt.Fprintf(w, "  会话：%d 个，共 %s；%d 天前的冷会话 %s\n", r.SessionCount, size(r.TotalBytes), agentkit.DefaultColdDays, size(r.ColdBytes))
		if cur := r.Current; cur != nil {
			est := ""
			if cur.Estimated {
				est = "（估算）"
			}
			fmt.Fprintf(w, "  最近的会话：%s，%.1f 天，%d token%s\n", cur.ID, cur.Days, cur.Tokens, est)
		}
		var days []string
		for _, d := range r.Daily {
			days = append(days, fmt.Sprint(d.Tokens))
		}
		fmt.Fprintf(w, "  最近 7 天每日 token：%s\n", strings.Join(days, " "))
	}
	for _, m := range r.Memory {
		fmt.Fprintf(w, "  记忆文件 %s：%d 行（阈值 %d）\n", m.Path, m.Lines, m.WarnLines)
	}
	for _, n := range r.Notes {
		fmt.Fprintf(w, "  · %s\n", n)
	}
	if len(r.Findings) == 0 {
		fmt.Fprintln(w, "没有发现问题。")
	}
	for _, f := range r.Findings {
		fmt.Fprintf(w, "发现：%s（%s）——%s\n", findingNames[f.ID], f.ID, f.Detail)
	}
	return nil
}

func (c *cli) proposals(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("proposals", flag.ContinueOnError)
	accept := fs.String("accept", "", "")
	acceptDemotion := fs.String("accept-demotion", "", "")
	reject := fs.String("reject", "", "")
	yes := fs.Bool("yes", false, "")
	pos, err := parseArgs(fs, args, 2)
	if err != nil {
		return err
	}
	host, instance := pos[0], pos[1]
	split := func(s string) []string {
		var out []string
		for _, x := range strings.Split(s, ",") {
			if x = strings.TrimSpace(x); x != "" {
				out = append(out, x)
			}
		}
		return out
	}
	if *accept == "" && *acceptDemotion == "" && *reject == "" {
		ps, err := c.svc.Proposals(ctx, host, instance)
		if err != nil {
			return err
		}
		if len(ps) == 0 {
			fmt.Fprintln(c.out, "没有待确认的归类提议。")
			return nil
		}
		print := func(title string, demotion bool) {
			first := true
			for _, p := range ps {
				if p.Demotion != demotion {
					continue
				}
				if first {
					fmt.Fprintln(c.out, title)
					first = false
				}
				fmt.Fprintf(c.out, "  %s  %s:%s → %s\n      理由（agent 提供）：%s\n", p.ID, p.Rule.Root, p.Rule.Glob, p.Rule.Category, p.Reason)
				if p.Unclassified > 0 {
					fmt.Fprintf(c.out, "      注意：其中 %d 个文件目前未分类（按凭据对待），接受后将进入轻装逃生舱；请确认里面没有凭据\n", p.Unclassified)
				}
			}
		}
		print("归类提议：", false)
		print("降级提议（接受后这些文件将不再进入 soul 档，需要单独确认）：", true)
		fmt.Fprintf(c.out, "\n接受：valise proposals %s %s --accept <id,…|all>\n", host, instance)
		fmt.Fprintf(c.out, "接受降级：valise proposals %s %s --accept-demotion <id,…>（逐个列出，不支持 all）\n", host, instance)
		fmt.Fprintf(c.out, "拒绝：valise proposals %s %s --reject <id,…|all>\n", host, instance)
		return nil
	}
	d := agentkit.DecideParams{InstanceID: instance, Accept: split(*accept), AcceptDemotions: split(*acceptDemotion), Reject: split(*reject)}
	for _, id := range d.AcceptDemotions {
		if id == "all" {
			return fmt.Errorf("降级提议必须逐个列出 id，不能用 all")
		}
	}
	if len(d.AcceptDemotions) > 0 && !*yes &&
		!c.confirm(fmt.Sprintf("接受降级提议 %s？这些文件将不再进入 soul 档（raw 档仍包含它们）", strings.Join(d.AcceptDemotions, ","))) {
		fmt.Fprintln(c.out, "已取消。")
		return nil
	}
	res, err := c.svc.DecideProposals(ctx, host, d)
	if err != nil {
		return err
	}
	for _, p := range res.Accepted {
		fmt.Fprintf(c.out, "已接受 %s：%s:%s → %s\n", p.ID, p.Rule.Root, p.Rule.Glob, p.Rule.Category)
	}
	for _, id := range res.Rejected {
		fmt.Fprintf(c.out, "已拒绝 %s\n", id)
	}
	if len(res.Accepted) > 0 {
		fmt.Fprintln(c.out, "从下一次建立逃生舱起生效。")
	}
	fmt.Fprintf(c.out, "剩余待确认：%d\n", res.Remaining)
	return nil
}

func (c *cli) kit(ctx context.Context, args []string) error {
	if len(args) > 0 && args[0] == "status" {
		return c.kitStatus(ctx, args[1:])
	}
	if len(args) > 0 && args[0] == "uninstall" {
		return c.kitUninstall(ctx, args[1:])
	}
	if len(args) > 0 && args[0] == "tools" {
		return c.kitTools(ctx, args[1:])
	}
	if len(args) > 0 && args[0] == "tool" {
		return c.kitTool(ctx, args[1:])
	}
	if len(args) == 0 || args[0] != "install" {
		return errUsage
	}
	fs := flag.NewFlagSet("kit", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "")
	pos, err := parseArgs(fs, args[1:], 2)
	if err != nil {
		return err
	}
	host, instance := pos[0], pos[1]
	plan, err := c.svc.KitInstall(ctx, host, instance, true)
	if err != nil {
		return err
	}
	if plan.Result != nil && len(plan.Result.Written) > 0 {
		fmt.Fprintf(c.out, "将把逃生舱技能写入 %s：\n", plan.Result.Dir)
		for _, p := range plan.Result.Written {
			fmt.Fprintf(c.out, "  %s\n", p)
		}
		for _, p := range plan.Result.Backups {
			fmt.Fprintf(c.out, "  （已有不同版本，先备份为 %s）\n", p)
		}
		if !*yes && !c.confirm("写入这些技能文件？") {
			fmt.Fprintln(c.out, "已取消，没有写入任何文件。")
			return nil
		}
		if plan, err = c.svc.KitInstall(ctx, host, instance, false); err != nil {
			return err
		}
		fmt.Fprintf(c.out, "已写入 %d 个技能文件。\n", len(plan.Result.Written))
	} else if plan.Result != nil {
		fmt.Fprintln(c.out, "技能已是最新，没有改动。")
	} else {
		fmt.Fprintf(c.out, "无法安装技能：%s\n", plan.NoSkills)
	}
	fmt.Fprintln(c.out, "\n接入 MCP（Valise 不会修改 harness 的配置，请自己执行或转告 agent）：")
	if len(plan.MCPCommand) > 0 {
		quoted := make([]string, len(plan.MCPCommand))
		for i, a := range plan.MCPCommand {
			quoted[i] = shellQuote(a)
		}
		fmt.Fprintf(c.out, "  在服务器 %s 上运行：\n    %s\n", host, strings.Join(quoted, " "))
	}
	if plan.MCPSnippet != "" {
		fmt.Fprintf(c.out, "  在服务器 %s 上把下面的配置加入 harness 的配置文件：\n", host)
		for _, l := range strings.Split(strings.TrimRight(plan.MCPSnippet, "\n"), "\n") {
			fmt.Fprintf(c.out, "    %s\n", l)
		}
	}
	if plan.MCPDoc != "" {
		fmt.Fprintf(c.out, "  配置说明：%s\n", plan.MCPDoc)
	}
	fmt.Fprintf(c.out, "  或者把这句话转告 agent：\n    %s\n", plan.AgentLine)
	fmt.Fprintln(c.out, "\n接入后对 agent 说：我给你装了一个逃生舱，你学习一下。")
	return nil
}

func shellQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./=:@${}", r))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

var skillStates = map[string]string{agentkit.SkillCurrent: "已安装（当前版本）", agentkit.SkillModified: "已安装（内容与当前版本不同）", agentkit.SkillMissing: "未安装"}

func (c *cli) kitStatus(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("kit status", flag.ContinueOnError)
	pos, err := parseArgs(fs, args, 2)
	if err != nil {
		return err
	}
	st, err := c.svc.KitStatus(ctx, pos[0], pos[1])
	if err != nil {
		return err
	}
	if st.Dir == "" {
		fmt.Fprintln(c.out, "这个实例的 profile 没有声明技能目录。")
	} else {
		fmt.Fprintf(c.out, "技能目录：%s\n", st.Dir)
		for _, s := range st.Skills {
			bak := ""
			if s.Backups > 0 {
				bak = fmt.Sprintf("，另有 %d 个 Valise 备份", s.Backups)
			}
			fmt.Fprintf(c.out, "  %-16s %s%s\n", s.Skill, skillStates[s.State], bak)
		}
	}
	if st.LastMCP != nil {
		fmt.Fprintf(c.out, "MCP：agent 最近一次经 MCP 调用是 %s（共 %d 次记录）\n", st.LastMCP.Local().Format("2006-01-02 15:04"), st.MCPCalls)
	} else {
		fmt.Fprintln(c.out, "MCP：还没有 agent 经 MCP 调用的记录（Valise 不读取 harness 的配置，无法确认是否已接入）")
	}
	return nil
}

func (c *cli) kitUninstall(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("kit uninstall", flag.ContinueOnError)
	force := fs.Bool("force", false, "")
	yes := fs.Bool("yes", false, "")
	pos, err := parseArgs(fs, args, 2)
	if err != nil {
		return err
	}
	host, instance := pos[0], pos[1]
	plan, err := c.svc.KitUninstall(ctx, host, instance, *force, true)
	if err != nil {
		return err
	}
	if len(plan.Result.Removed) == 0 {
		fmt.Fprintln(c.out, "没有可删除的逃生舱技能文件。")
	} else {
		fmt.Fprintln(c.out, "将删除：")
		for _, p := range plan.Result.Removed {
			fmt.Fprintf(c.out, "  %s\n", p)
		}
	}
	for _, p := range plan.Result.Kept {
		fmt.Fprintf(c.out, "保留（内容已被修改；确认不需要时加 --force）：%s\n", p)
	}
	if len(plan.Result.Removed) > 0 {
		if !*yes && !c.confirm("删除这些文件？") {
			fmt.Fprintln(c.out, "已取消，没有删除任何文件。")
			return nil
		}
		if plan, err = c.svc.KitUninstall(ctx, host, instance, *force, false); err != nil {
			return err
		}
		fmt.Fprintf(c.out, "已删除 %d 个文件。\n", len(plan.Result.Removed))
	}
	fmt.Fprintln(c.out, "\n撤除 MCP（Valise 不会修改 harness 的配置）：")
	switch {
	case len(plan.MCPCommand) > 0:
		quoted := make([]string, len(plan.MCPCommand))
		for i, a := range plan.MCPCommand {
			quoted[i] = shellQuote(a)
		}
		fmt.Fprintf(c.out, "  在服务器 %s 上运行：\n    %s\n", host, strings.Join(quoted, " "))
	case plan.MCPSnippet:
		fmt.Fprintln(c.out, "  从当初粘贴配置片段的文件中删除名为 valise 的 MCP 条目（valise kit install 打印过它的位置）。")
	default:
		fmt.Fprintf(c.out, "  在 harness 的 MCP 配置中删除名为 %s 的服务器，或请 agent 删除。\n", plan.MCPServerName)
	}
	fmt.Fprintln(c.out, "另外，agent 学习逃生舱时可能在它的常驻指令里加过一行提及 valise_seal 的提醒，请自行删除或让 agent 删除。")
	return nil
}
