package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"valise/core/agentkit"
	"valise/ui/service"
)

// chatTab is the terminal UI's way to talk to the agent: quick entries,
// not a chat of its own (docs/UI.md 9.5 "对话", 2026-11-03): open the
// harness's own chat, or copy what to say to use a Valise skill. It can be
// turned off in settings; the full chat belongs to the GUI (M16).
type chatTab struct{ cursor int }

// chatTabOn is the settings switch, read when an agent page is built.
var chatTabOn = true

// agentTabs puts the chat tab second when it is on.
func agentTabs(rest ...tab) []tab {
	if !chatTabOn {
		return rest
	}
	return append([]tab{rest[0], &chatTab{}}, rest[1:]...)
}

// skillPrompts are the words that start each Valise skill (agent-kit/skills).
var skillPrompts = []struct{ skill, label, say string }{
	{"valise-onboard", "连接验收与报备目录布局", "用 valise-onboard 验收连接并报备你的目录布局。"},
	{"valise-backup", "查询当前 Valise 备份状态", "用 valise-backup 看看你现在的 Valise 备份状态。"},
	{"valise-backup", "高风险改动前先留个底", "动手改之前，先用 valise-backup 帮我留个底。"},
}

type chatEntry struct {
	label string
	do    func(m *Model, p *instancePage) tea.Cmd
}

func (c *chatTab) entries(p *instancePage) []chatEntry {
	host, inst := p.host, p.inst.InstanceID
	var out []chatEntry
	if p.inst.NativeCLI && p.target != nil && len(p.target.Profile.NativeCLI) > 0 {
		out = append(out, chatEntry{"打开 harness 自己的对话（原生界面：" + strings.Join(p.target.Profile.NativeCLI, " ") + "）", func(m *Model, p *instancePage) tea.Cmd {
			return gatedCLI(m, host, "native", host, "--instance", p.inst.InstanceID)
		}})
	}
	if len(p.inst.Entries) > 0 {
		out = append(out, chatEntry{"打开管理页（在浏览器里对话）", func(m *Model, p *instancePage) tea.Cmd { return m.runCLI("open", host, inst) }})
	}
	installed := map[string]bool{}
	if p.kit != nil {
		for _, s := range p.kit.Skills {
			installed[s.Skill] = s.State == agentkit.SkillCurrent
		}
	}
	for _, sp := range skillPrompts {
		if !installed[sp.skill] {
			continue
		}
		say := sp.say
		out = append(out, chatEntry{"复制：" + sp.label + "（「" + say + "」）", func(m *Model, p *instancePage) tea.Cmd {
			m.flash, m.flashErr = "已复制到剪贴板（终端支持时）："+say+"，粘贴给 agent 就行", false
			return tea.SetClipboard(say)
		}})
	}
	return out
}

func (c *chatTab) name() string { return "对话" }
func (c *chatTab) help(p *instancePage) string {
	return menu("↑↓", "选择", "enter", "打开 / 复制")
}
func (c *chatTab) load(m *Model, p *instancePage) tea.Cmd { return nil }
func (c *chatTab) update(m *Model, p *instancePage, msg tea.Msg) (tea.Cmd, bool) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil, false
	}
	es := c.entries(p)
	if cur, moved := moveList(c.cursor, len(es), k.String()); moved {
		c.cursor = cur
		return nil, true
	}
	if k.String() == "enter" && len(es) > 0 {
		return es[min(c.cursor, len(es)-1)].do(m, p), true
	}
	return nil, false
}

func (c *chatTab) view(m *Model, p *instancePage, height int) string {
	var b strings.Builder
	if a := approvalNow(p); a != "" {
		b.WriteString(warnStyle.Render("agent 动手时用的是它自己的权限。") + a + "\n\n")
	}
	es := c.entries(p)
	c.cursor = min(c.cursor, max(0, len(es)-1))
	for i, e := range es {
		if i == c.cursor {
			n := strings.Count(b.String(), "\n")
			p.setFocus(n, n)
		}
		b.WriteString("  " + cursorLine(i == c.cursor, e.label) + "\n")
	}
	if len(es) == 0 {
		b.WriteString(dimStyle.Render("这个 harness 没有登记原生对话或管理页，Agent 保护技能也还没装好。") + "\n")
	}
	b.WriteString("\n" + dimStyle.Render("用哪种方式和 agent 说话都一样，它收到的是同一句话；Valise 不附加提示词，也不保存聊天记录。") + "\n")
	b.WriteString(faintStyle.Render("不需要这个分栏时，可以在 设置 › 高级 里关掉。"))
	return b.String()
}

// approvalNow is the approval state from the health report, "" unknown.
func approvalNow(p *instancePage) string {
	if p.health == nil {
		return ""
	}
	for _, it := range p.health.Items {
		if it.ID != "approval" {
			continue
		}
		switch it.Level {
		case service.HealthBad:
			return "它现在处于 " + errStyle.Render("yolo 状态") + "：执行风险命令" + errStyle.Render("不需要你批准") + "（Bypass），你对它说的话它可能直接照做。"
		case service.HealthOK:
			return "它现在执行风险命令前需要你批准。"
		}
	}
	return ""
}
