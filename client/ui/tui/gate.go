package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// gateThen opens next once an advanced feature may run (docs/INSTALL.md
// 4.2): refused without an escape pod kept elsewhere; a stale one is shown
// and can be accepted. instance "" checks every agent on the server.
func gateThen(m *Model, host, instance string, next func() screen) tea.Cmd {
	svc, ctx := m.svc, m.ctx
	m.flash, m.flashErr = "正在确认别处有能独立恢复的逃生舱…", false
	return func() tea.Msg {
		g, err := svc.AdvancedGate(ctx, host, instance)
		if err == nil {
			err = g.Err()
		}
		if err != nil {
			return failed(err)
		}
		if w := g.Warning(); w != "" {
			return pushMsg{confirm("别处的逃生舱有些旧了，知道这个风险，仍然继续？", strings.Split(w, "\n"),
				func(m *Model) tea.Cmd { return m.push(next()) })}
		}
		return pushMsg{next()}
	}
}
