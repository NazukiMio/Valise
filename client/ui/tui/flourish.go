package tui

import "charm.land/lipgloss/v2"

// Flourish lines (docs/UI.md section 9): at most one per page, as a
// subtitle under the title or at the bottom right near the frame. They
// say only what is true of the page, never sit in buttons, confirmations
// or errors, and are dropped first when space is short. All the copy is
// here so it can be changed in one place.

var flourishStyle = lipgloss.NewStyle().Foreground(cFaint).Italic(true)

// flourisher is a screen with a bottom-right flourish line.
type flourisher interface{ flourish(m *Model) string }

// subtitle renders a flourish as a subtitle line.
func subtitle(s string) string { return flourishStyle.Render("——" + s) }

func flourishHome(servers int) string {
	if servers == 0 {
		return "箱子还空着，先接入一台服务器吧。"
	}
	return "行李都在，随时可以出发。"
}

func flourishServer(agents int) string {
	if agents == 0 {
		return ""
	}
	return "这一站停靠着 " + itoa(agents) + " 个 agent。"
}

func flourishConnected(instance string) string {
	return "把 " + instance + " 塞进 Valise 了。之后它的运行状态、管理页和备份都在这里。"
}

const (
	flourishOverview = "它在路上，一切安好。"
	flourishManage   = "工具都在箱子的夹层里。"
	flourishPods     = "每一份备份，都是一张回程票。"
	flourishSealed   = "又多了一张回程票。"
	flourishLanded   = "平安抵达。"
	flourishKeys     = "钥匙只在你手里。"
)
