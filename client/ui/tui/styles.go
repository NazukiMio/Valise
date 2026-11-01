package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
)

// Palette (docs/UI.md section 9): one accent, semantic colors apart.
var (
	accent   = lipgloss.Color("#F0A35E")
	cOK      = lipgloss.Color("#8FC57A")
	cWarn    = lipgloss.Color("#E6C069")
	cBad     = lipgloss.Color("#E57A6F")
	cInfo    = lipgloss.Color("#7FB0E6")
	cDim     = lipgloss.Color("#858A94")
	cFaint   = lipgloss.Color("#555B66")
	cLine    = lipgloss.Color("#3A3F4A")
	cSelLine = accent
	cRobot   = lipgloss.Color("#9FB3C8")
)

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(accent)
	headStyle  = lipgloss.NewStyle().Bold(true)
	dimStyle   = lipgloss.NewStyle().Foreground(cDim)
	faintStyle = lipgloss.NewStyle().Foreground(cFaint)
	okStyle    = lipgloss.NewStyle().Foreground(cOK)
	warnStyle  = lipgloss.NewStyle().Foreground(cWarn)
	errStyle   = lipgloss.NewStyle().Foreground(cBad)
	infoStyle  = lipgloss.NewStyle().Foreground(cInfo)
	accStyle   = lipgloss.NewStyle().Foreground(accent)
	keyStyle   = lipgloss.NewStyle().Foreground(accent).Bold(true)
	selStyle   = lipgloss.NewStyle().Bold(true).Foreground(accent)
)

// cursorLine renders a list line with the selection marker.
func cursorLine(selected bool, s string) string {
	if selected {
		return selStyle.Render("▸ ") + s
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

// menu renders the footer: "k 说明" pairs.
func menu(items ...string) string {
	var parts []string
	for i := 0; i+1 < len(items); i += 2 {
		parts = append(parts, keyStyle.Render(items[i])+" "+items[i+1])
	}
	return strings.Join(parts, "   ")
}

// box draws a section with a title.
func box(title, body string, width int) string {
	st := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cLine).Padding(0, 1)
	if width > 4 {
		st = st.Width(width)
	}
	if title != "" {
		body = headStyle.Render(title) + "\n" + body
	}
	return st.Render(body)
}

// kv renders aligned label/value rows.
func kv(rows ...string) string {
	w := 0
	for i := 0; i+1 < len(rows); i += 2 {
		w = max(w, lipgloss.Width(rows[i]))
	}
	var b strings.Builder
	for i := 0; i+1 < len(rows); i += 2 {
		pad := strings.Repeat(" ", w-lipgloss.Width(rows[i]))
		b.WriteString(dimStyle.Render(rows[i]) + pad + "  " + rows[i+1])
		if i+2 < len(rows) {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// size renders a byte count.
func size(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// ago renders how long ago t was, in words.
func ago(t time.Time, now time.Time) string {
	d := now.Sub(t)
	switch {
	case t.IsZero():
		return "从未"
	case d < time.Minute:
		return "刚刚"
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟前", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 小时前", int(d.Hours()))
	case d < 60*24*time.Hour:
		return fmt.Sprintf("%d 天前", int(d.Hours()/24))
	}
	return t.Local().Format("2006-01-02")
}

// dayLabel groups dates for lists: 今天 / 昨天 / 10月28日 / 2025年…
func dayLabel(t, now time.Time) string {
	t, now = t.Local(), now.Local()
	y1, m1, d1 := t.Date()
	y2, m2, d2 := now.Date()
	switch {
	case y1 == y2 && m1 == m2 && d1 == d2:
		return "今天"
	case y1 == y2 && t.YearDay() == now.YearDay()-1:
		return "昨天"
	case y1 == y2:
		return fmt.Sprintf("%d月%d日", m1, d1)
	}
	return fmt.Sprintf("%d年%d月%d日", y1, m1, d1)
}

// clip cuts s to at most n lines.
func clip(s string, n int) string {
	if n <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
