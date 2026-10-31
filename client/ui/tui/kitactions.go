package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"valise/core/agentkit"
)

var skillStates = map[string]string{agentkit.SkillCurrent: "已安装（当前版本）", agentkit.SkillModified: "已安装（内容与当前版本不同）", agentkit.SkillMissing: "未安装"}

func (k *kitScreen) update(m *Model, msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case kitStatusLoaded:
		if msg.err != nil {
			return func() tea.Msg { return failed(msg.err) }, true
		}
		k.st = msg.st
		return nil, true
	case kitPlanned:
		if msg.err != nil {
			return func() tea.Msg { return failed(msg.err) }, true
		}
		k.plan, k.removal = msg.install, msg.removal
		if msg.done {
			return k.init(m), true
		}
		return k.confirmPlan(m), true
	case tea.KeyPressMsg:
		switch msg.String() {
		case "i":
			return func() tea.Msg {
				p, err := m.svc.KitInstall(m.ctx, k.host, k.inst, true)
				return kitPlanned{install: p, err: err}
			}, true
		case "u", "F":
			force := msg.String() == "F"
			k.force = force
			return func() tea.Msg {
				p, err := m.svc.KitUninstall(m.ctx, k.host, k.inst, force, true)
				return kitPlanned{removal: p, err: err}
			}, true
		default:
			return nil, false
		}
	}
	return nil, false
}

// confirmPlan shows the dry run and asks before writing or deleting.
func (k *kitScreen) confirmPlan(m *Model) tea.Cmd {
	switch {
	case k.plan != nil && k.plan.Result != nil && len(k.plan.Result.Written) > 0:
		detail := []string{"将写入：" + k.plan.Result.Dir}
		for _, p := range k.plan.Result.Written {
			detail = append(detail, "  "+p)
		}
		for _, p := range k.plan.Result.Backups {
			detail = append(detail, "  （已有不同版本，先备份为 "+p+"）")
		}
		return m.push(confirm("写入这些技能文件？", detail, func(m *Model) tea.Cmd {
			return func() tea.Msg {
				p, err := m.svc.KitInstall(m.ctx, k.host, k.inst, false)
				return kitPlanned{install: p, err: err, done: true}
			}
		}))
	case k.removal != nil && len(k.removal.Result.Removed) > 0:
		detail := []string{"将删除："}
		for _, p := range k.removal.Result.Removed {
			detail = append(detail, "  "+p)
		}
		for _, p := range k.removal.Result.Kept {
			detail = append(detail, "  保留（内容已被修改；需要时按 F）："+p)
		}
		force := k.force
		return m.push(confirm("删除这些文件？", detail, func(m *Model) tea.Cmd {
			return func() tea.Msg {
				p, err := m.svc.KitUninstall(m.ctx, k.host, k.inst, force, false)
				return kitPlanned{removal: p, err: err, done: true}
			}
		}))
	}
	return func() tea.Msg { return flashMsg("没有需要写入或删除的文件") }
}

func (k *kitScreen) view(m *Model) string {
	var b strings.Builder
	if st := k.st; st == nil {
		b.WriteString(dimStyle.Render("读取中…") + "\n")
	} else {
		if st.Dir == "" {
			b.WriteString("这个实例的 profile 没有声明技能目录。\n")
		} else {
			fmt.Fprintf(&b, "技能目录：%s\n", st.Dir)
			for _, s := range st.Skills {
				bak := ""
				if s.Backups > 0 {
					bak = dimStyle.Render(fmt.Sprintf("（另有 %d 个 Valise 备份）", s.Backups))
				}
				fmt.Fprintf(&b, "  %-16s %s%s\n", s.Skill, skillStates[s.State], bak)
			}
		}
		if st.LastMCP != nil {
			fmt.Fprintf(&b, "MCP：agent 最近一次经 MCP 调用 %s（共 %d 次记录）\n", st.LastMCP.Local().Format("2006-01-02 15:04"), st.MCPCalls)
		} else {
			b.WriteString(dimStyle.Render("MCP：还没有 agent 经 MCP 调用的记录（Valise 不读取 harness 的配置）") + "\n")
		}
	}
	if p := k.plan; p != nil {
		b.WriteString("\n接入 MCP（Valise 不会修改 harness 的配置，请自己执行或转告 agent）：\n")
		if len(p.MCPCommand) > 0 {
			b.WriteString("  " + strings.Join(p.MCPCommand, " ") + "\n")
		}
		if p.MCPSnippet != "" {
			for _, l := range strings.Split(strings.TrimRight(p.MCPSnippet, "\n"), "\n") {
				b.WriteString("  " + l + "\n")
			}
		}
		b.WriteString("  或转告 agent：" + p.AgentLine + "\n")
	}
	if r := k.removal; r != nil {
		b.WriteString("\n撤除 MCP（Valise 不会修改 harness 的配置）：\n")
		switch {
		case len(r.MCPCommand) > 0:
			b.WriteString("  " + strings.Join(r.MCPCommand, " ") + "\n")
		case r.MCPSnippet:
			b.WriteString("  从当初粘贴配置片段的文件中删除名为 valise 的 MCP 条目\n")
		}
		b.WriteString(dimStyle.Render("  agent 可能在常驻指令里加过一行提及 valise_seal 的提醒，请自行删除或让 agent 删除") + "\n")
	}
	return b.String()
}
