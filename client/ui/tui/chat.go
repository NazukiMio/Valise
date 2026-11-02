package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"valise/ui/service"
)

// chatTab is where talking to the agent goes (docs/UI.md 9.5 "对话"). Its
// place and wording are set now; sending and receiving come with M16, once
// a profile's chat capability is verified on a target.
type chatTab struct{}

func (c *chatTab) name() string                                                  { return "对话" }
func (c *chatTab) help(p *instancePage) string                                   { return "" }
func (c *chatTab) load(m *Model, p *instancePage) tea.Cmd                        { return nil }
func (c *chatTab) update(m *Model, p *instancePage, msg tea.Msg) (tea.Cmd, bool) { return nil, false }

func (c *chatTab) view(m *Model, p *instancePage, height int) string {
	var b strings.Builder
	b.WriteString(accStyle.Render("在 Valise 里和这个 agent 对话") + "\n\n")
	b.WriteString("Valise 只把你打的字原样转给 agent，再把它的回复原样显示出来。\n")
	b.WriteString("不附加任何提示词，不保存聊天记录（agent 自己会照常记录）。\n")
	b.WriteString(dimStyle.Render("你也可以照常用别的方式和它对话（管理页、聊天软件、harness 自己的命令行）。在这里说和在那些地方说") + "\n")
	b.WriteString(dimStyle.Render("是一样的，agent 收到的是同一句话；Valise 只是多了一个入口。") + "\n\n")
	b.WriteString(warnStyle.Render("agent 动手时用的是它自己的权限。") + approvalNow(p) + "\n\n")
	b.WriteString(faintStyle.Render("这个 harness 的对话方式还没有核实，对话会在之后的版本里开放。"))
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
			return "它现在处于 " + errStyle.Render("yolo 状态") + "：执行风险命令" + errStyle.Render("不需要你批准") + "（Bypass），你在这里说的话它可能直接照做。"
		case service.HealthOK:
			return "它现在执行风险命令前需要你批准。"
		}
	}
	return ""
}
