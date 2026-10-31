package tui

import "charm.land/lipgloss/v2"

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	headStyle  = lipgloss.NewStyle().Bold(true)
	dimStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	okStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	warnStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	errStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	selStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("5"))
)

// cursorLine renders a list line with the selection marker.
func cursorLine(selected bool, s string) string {
	if selected {
		return selStyle.Render("› ") + s
	}
	return "  " + s
}

// mark renders a state as ✓ / ✗ / · with color.
func mark(ok bool, known bool) string {
	switch {
	case !known:
		return dimStyle.Render("·")
	case ok:
		return okStyle.Render("✓")
	default:
		return errStyle.Render("✗")
	}
}
