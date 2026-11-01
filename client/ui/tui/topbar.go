package tui

import (
	"strings"
	"time"

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
	return dimStyle.Render("管理权限：已锁定")
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
