package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

func scrollAction(key string, offset *int, height int) bool {
	switch key {
	case "up", "k":
		*offset = max(0, *offset-1)
	case "down", "j":
		*offset++
	case "pgup":
		*offset = max(0, *offset-max(1, height/2))
	case "pgdown":
		*offset += max(1, height/2)
	case "home":
		*offset = 0
	case "end":
		*offset = 1 << 20
	default:
		return false
	}
	return true
}

// Wrap before counting physical rows, so long argv cannot hide later steps.
func actionViewport(m *Model, text string, offset *int, height int) string {
	height = max(2, height)
	wrapped := lipgloss.NewStyle().Width(max(1, m.width-2)).Render(text)
	rows := strings.Split(wrapped, "\n")
	if len(rows) <= height {
		*offset = 0
		return wrapped
	}
	room := height - 1
	*offset = min(max(0, *offset), max(0, len(rows)-room))
	visible := append([]string{}, rows[*offset:min(len(rows), *offset+room)]...)
	return strings.Join(append(visible, dimStyle.Render("PgUp/PgDn 滚动 · "+itoa(*offset+1)+"–"+itoa(min(len(rows), *offset+room))+" / "+itoa(len(rows)))), "\n")
}
