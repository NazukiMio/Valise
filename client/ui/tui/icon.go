package tui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"valise/core/profile"
	"valise/ui/iconart"
)

// Instance icons (docs/UI.md 8.1); the art itself is in package iconart,
// shared with the desktop.
const (
	iconW        = iconart.StdW
	iconH        = iconart.StdH
	compactIconW = iconart.CompactW
	compactIconH = iconart.CompactH
)

// iconStyle colors an icon with the profile's color.
func iconStyle(d *profile.Display) lipgloss.Style {
	if iconart.Own(d) && d.Color != "" {
		return lipgloss.NewStyle().Foreground(lipgloss.Color(d.Color))
	}
	return lipgloss.NewStyle().Foreground(cRobot)
}

// standardIcon is the icon centered in a 24×12 box.
func standardIcon(d *profile.Display) []string {
	return boxArt(iconart.Standard(d), iconW, iconH, iconStyle(d))
}

// compactIcon is the icon centered in a 12×6 box.
func compactIcon(d *profile.Display) []string {
	return boxArt(iconart.Compact(d), compactIconW, compactIconH, iconStyle(d))
}

func fitIcon(lines []string, w, h int) []string { return iconart.Fit(lines, w, h) }

func splitArt(s string) []string { return iconart.Split(s) }

// boxArt centers art in w×h and colors it; every line is exactly w wide.
func boxArt(art []string, w, h int, st lipgloss.Style) []string {
	top := max(0, (h-len(art))/2)
	out := make([]string, h)
	for i := range out {
		line := ""
		if j := i - top; j >= 0 && j < len(art) {
			line = art[j]
		}
		lw := lipgloss.Width(line)
		if lw > w {
			line, lw = string([]rune(line)[:w]), w
		}
		left := (w - lw) / 2
		out[i] = strings.Repeat(" ", left) + st.Render(line) + strings.Repeat(" ", w-lw-left)
	}
	return out
}

// brailleBit is the braille dot bit for (x, y); used by the scenes too.
var brailleBit = iconart.BrailleBit
