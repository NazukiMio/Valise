package tui

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
)

// window returns the slice [from, to) of n rows that keeps cursor visible
// in height rows (docs/UI.md section 8: the cursor never leaves the screen).
func window(n, cursor, height int) (from, to int) {
	if height <= 0 || n <= height {
		return 0, n
	}
	from = cursor - height/2
	from = max(0, min(from, n-height))
	return from, from + height
}

// scrollLines renders rows in a viewport of height lines around cursor,
// with a position hint when not everything fits.
func scrollLines(rows []string, cursor, height int) string {
	return scrollLinesHint(rows, cursor, height, "第 "+itoa(cursor+1)+" / "+itoa(len(rows))+" 项")
}

// scrollLinesHint is scrollLines with the caller's position text (for
// lists with header rows).
func scrollLinesHint(rows []string, cursor, height int, hint string) string {
	if len(rows) <= height {
		return strings.Join(rows, "\n")
	}
	from, to := window(len(rows), cursor, height-1)
	out := append([]string{}, rows[from:to]...)
	out = append(out, faintStyle.Render("  ↑↓ "+hint))
	return strings.Join(out, "\n")
}

func itoa(n int) string { return strconv.Itoa(n) }

// card styles.
func cardStyle(selected, muted bool, width int) lipgloss.Style {
	c := cLine
	if selected {
		c = cSelLine
	}
	st := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(c).Padding(0, 1).Width(width)
	if muted {
		st = st.Foreground(cDim)
	}
	return st
}

// cardWidth is the outer width of grid cards.
const cardWidth = 40

// gridCols is how many cards fit the width.
func gridCols(width int) int {
	return max(1, (width+1)/(cardWidth+1))
}

// grid lays cards out in rows that fit width, scrolled by whole rows so
// the selected card stays within height lines.
func grid(cards []string, selected, width, height int) string {
	if len(cards) == 0 {
		return ""
	}
	cols := gridCols(width)
	var rows []string
	for i := 0; i < len(cards); i += cols {
		end := min(i+cols, len(cards))
		h := 0
		for _, c := range cards[i:end] {
			h = max(h, lipgloss.Height(c))
		}
		var cells []string
		for _, c := range cards[i:end] {
			cells = append(cells, lipgloss.NewStyle().Height(h).Render(c))
		}
		rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, intersperse(cells, " ")...))
	}
	selRow := selected / cols
	// Drop whole rows above the selection until the selection fits.
	from := 0
	for from < selRow && sumHeight(rows[from:selRow+1]) > height {
		from++
	}
	out := strings.Join(rows[from:], "\n")
	if from > 0 {
		out = faintStyle.Render("  ↑ 上面还有") + "\n" + out
	}
	return out
}

func sumHeight(rows []string) int {
	n := 0
	for _, r := range rows {
		n += lipgloss.Height(r)
	}
	return n
}

func intersperse(cells []string, sep string) []string {
	var out []string
	for i, c := range cells {
		if i > 0 {
			out = append(out, sep)
		}
		out = append(out, c)
	}
	return out
}

// moveGrid moves a grid cursor by key.
func moveGrid(cursor, n, cols int, key string) (int, bool) {
	switch key {
	case "left":
		cursor--
	case "right":
		cursor++
	case "up":
		cursor -= cols
	case "down":
		cursor += cols
	default:
		return cursor, false
	}
	return max(0, min(n-1, cursor)), true
}

// moveList moves a list cursor by key.
func moveList(cursor, n int, key string) (int, bool) {
	switch key {
	case "up", "k":
		cursor--
	case "down", "j":
		cursor++
	case "pgup":
		cursor -= 10
	case "pgdown":
		cursor += 10
	case "home", "g":
		cursor = 0
	case "end", "G":
		cursor = n - 1
	default:
		return cursor, false
	}
	return max(0, min(n-1, cursor)), true
}

// tabs renders a tab bar.
func tabs(names []string, active int) string {
	var parts []string
	for i, n := range names {
		label := itoa(i+1) + " " + n
		if i == active {
			parts = append(parts, lipgloss.NewStyle().Bold(true).Foreground(accent).Underline(true).Render(label))
		} else {
			parts = append(parts, dimStyle.Render(label))
		}
	}
	return strings.Join(parts, "   ")
}
