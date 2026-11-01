package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// hosted is a screen that belongs to one server.
type hosted interface{ hostName() string }

// currentHost is the server of the innermost screen that has one.
func (m *Model) currentHost() string {
	for i := len(m.stack) - 1; i >= 0; i-- {
		if h, ok := m.stack[i].(hosted); ok {
			return h.hostName()
		}
	}
	return ""
}

// accessState is the top bar's right side (docs/UI.md section 8): full
// access mode in red, or the admin window of the current server.
func (m *Model) accessState() string {
	if fa := m.svc.FullAccessHost(); fa != "" {
		left, _ := m.svc.AdminRemaining(fa)
		return lipgloss.NewStyle().Background(cBad).Foreground(lipgloss.Color("#FFFFFF")).Bold(true).
			Render(" 完全访问模式 · " + fa + " · 剩余 " + minutes(left) + " ")
	}
	host := m.currentHost()
	if host == "" || !m.svc.HasAdminKey(host) {
		return ""
	}
	if left, ok := m.svc.AdminRemaining(host); ok {
		return okStyle.Render("管理权限已解锁 · 剩余 "+minutes(left)) + dimStyle.Render(" · ") + keyStyle.Render("L") + dimStyle.Render(" 立即锁定")
	}
	return dimStyle.Render("管理权限：已锁定 · ") + keyStyle.Render("U") + dimStyle.Render(" 解锁")
}

func minutes(d time.Duration) string { return itoa(max(1, int(d.Minutes()+0.5))) + " 分钟" }

// topBar puts left and right on one line of width.
func topBar(left, right string, width int) string {
	gap := min(width, 120) - lipgloss.Width(left) - lipgloss.Width(right)
	if right == "" || gap < 2 {
		return left
	}
	return left + strings.Repeat(" ", gap) + right
}

// unlockAdmin is the U key: unlock the current server's admin key for the
// admin window on purpose, instead of waiting for an admin action to ask.
func (m *Model) unlockAdmin() tea.Cmd {
	host := m.currentHost()
	if host == "" || m.svc.FullAccessHost() != "" {
		return nil
	}
	if !m.svc.Tiered(host) {
		m.flash, m.flashErr = host+" 用你自己的 SSH 设置连接，管理操作直接用你的登录方式，没有单独的管理权限可以解锁", false
		return nil
	}
	if !m.svc.HasAdminKey(host) {
		return m.push(confirm("为 "+host+" 创建管理钥匙？", []string{
			host + " 还没有管理钥匙：每次管理操作都要在终端里输入服务器密码，终端界面里没法输入。",
			"管理钥匙用单独的管理口令加密；解锁后 " + itoa(int(m.svc.AdminWindow().Minutes())) + " 分钟内的管理操作不再询问。",
			"创建时会在终端里用服务器密码登录一次。"},
			func(m *Model) tea.Cmd { return m.runCLI("server", "admin-key", host) }))
	}
	if _, ok := m.svc.AdminRemaining(host); ok {
		m.flash, m.flashErr = "管理权限已经是解锁状态；按 L 立即锁定", false
		return nil
	}
	svc, ctx := m.svc, m.ctx
	return func() tea.Msg {
		if err := svc.UnlockAdmin(ctx, host); err != nil {
			return failed(err)
		}
		return flashMsg("已解锁 " + host + " 的管理权限，" + itoa(int(svc.AdminWindow().Minutes())) + " 分钟内的管理操作不再询问；按 L 立即锁定")
	}
}

// typing reports that the screen on top is taking text, so global letter
// keys (U, L) are left to it.
func (m *Model) typing() bool {
	switch t := m.top().(type) {
	case *inputScreen:
		return true
	case interface{ typing() bool }:
		return t.typing()
	}
	return false
}

// wrapMenu adds esc/q and wraps the footer's key pairs onto as many lines
// as the width needs (docs/UI.md section 8: two lines at 80 columns).
func wrapMenu(help string, width int, back bool) string {
	if back {
		help += "   " + menu("esc", "返回")
	} else {
		help += "   " + menu("q", "退出")
	}
	width = min(width, 120)
	var lines []string
	cur := ""
	for _, item := range strings.Split(help, "   ") {
		if cur != "" && lipgloss.Width(cur)+3+lipgloss.Width(item) > width {
			lines, cur = append(lines, cur), item
			continue
		}
		if cur != "" {
			cur += "   "
		}
		cur += item
	}
	return strings.Join(append(lines, cur), "\n")
}
