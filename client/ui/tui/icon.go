package tui

import (
	_ "embed"
	"strings"

	"charm.land/lipgloss/v2"

	"valise/core/profile"
)

// Instance icons (docs/UI.md 8.1). Profiles keep the harness's art at the
// standard size; the compact icon is the profile's own or, failing that,
// the standard one scaled down. Harnesses without art get the built-in
// robot, drawn by the user (copies of docs/robot-icon-*.txt).
const (
	iconW        = 24
	iconH        = 12
	compactIconW = 12
	compactIconH = 6
)

var (
	//go:embed robot-24x12.txt
	robotStd string
	//go:embed robot-12x6.txt
	robotCompact string
)

// iconStyle colors an icon with the profile's color.
func iconStyle(d *profile.Display) lipgloss.Style {
	if d != nil && d.Color != "" && strings.TrimSpace(d.Icon+d.IconCompact) != "" {
		return lipgloss.NewStyle().Foreground(lipgloss.Color(d.Color))
	}
	return lipgloss.NewStyle().Foreground(cRobot)
}

// standardIcon is the icon centered in a 24×12 box.
func standardIcon(d *profile.Display) []string {
	art := splitArt(robotStd)
	if d != nil && strings.TrimSpace(d.Icon) != "" {
		art = fitIcon(splitArt(d.Icon), iconW, iconH)
	}
	return boxArt(art, iconW, iconH, iconStyle(d))
}

// compactIcon is the icon centered in a 12×6 box.
func compactIcon(d *profile.Display) []string {
	art := splitArt(robotCompact)
	switch {
	case d != nil && strings.TrimSpace(d.IconCompact) != "":
		art = fitIcon(splitArt(d.IconCompact), compactIconW, compactIconH)
	case d != nil && strings.TrimSpace(d.Icon) != "":
		art = fitIcon(splitArt(d.Icon), compactIconW, compactIconH)
	}
	return boxArt(art, compactIconW, compactIconH, iconStyle(d))
}

func splitArt(s string) []string { return strings.Split(strings.TrimRight(s, "\n"), "\n") }

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

// fitIcon scales art into w×h after trimming blank borders: braille art by
// the share of dots in each block, other art by sampling characters.
func fitIcon(lines []string, w, h int) []string {
	grid := trimArt(lines)
	if len(grid) == 0 {
		return nil
	}
	gw := 0
	for _, r := range grid {
		gw = max(gw, len(r))
	}
	if gw <= w && len(grid) <= h {
		return toStrings(grid)
	}
	if isBraille(grid) {
		return scaleBraille(grid, gw, w, h)
	}
	return sampleArt(grid, gw, w, h)
}

func blank(r rune) bool { return r == ' ' || r == '⠀' }

func trimArt(lines []string) [][]rune {
	var g [][]rune
	for _, l := range lines {
		g = append(g, []rune(l))
	}
	rowBlank := func(r []rune) bool {
		for _, c := range r {
			if !blank(c) {
				return false
			}
		}
		return true
	}
	for len(g) > 0 && rowBlank(g[0]) {
		g = g[1:]
	}
	for len(g) > 0 && rowBlank(g[len(g)-1]) {
		g = g[:len(g)-1]
	}
	left := -1
	for _, r := range g {
		for i, c := range r {
			if !blank(c) {
				if left < 0 || i < left {
					left = i
				}
				break
			}
		}
	}
	for i, r := range g {
		if left > 0 && len(r) >= left {
			r = r[left:]
		}
		for len(r) > 0 && blank(r[len(r)-1]) {
			r = r[:len(r)-1]
		}
		g[i] = r
	}
	return g
}

func toStrings(g [][]rune) []string {
	out := make([]string, len(g))
	for i, r := range g {
		out[i] = string(r)
	}
	return out
}

func isBraille(g [][]rune) bool {
	n, b := 0, 0
	for _, r := range g {
		for _, c := range r {
			if c != ' ' {
				n++
				if c >= 0x2800 && c <= 0x28FF {
					b++
				}
			}
		}
	}
	return n > 0 && b*10 >= n*9
}

// Braille dot bit for (x in 0..1, y in 0..3).
var brailleBit = [4][2]uint8{{0x01, 0x08}, {0x02, 0x10}, {0x04, 0x20}, {0x40, 0x80}}

// scaleBraille shrinks braille art by an integer factor; a target dot is
// set when at least half of its block is set (UI.md 8.1: not "any dot",
// which would fill in the inside of the drawing).
func scaleBraille(g [][]rune, gw, w, h int) []string {
	pw, ph := gw*2, len(g)*4
	dots := make([][]bool, ph)
	for y := range dots {
		dots[y] = make([]bool, pw)
	}
	for cy, r := range g {
		for cx, c := range r {
			if c < 0x2800 || c > 0x28FF {
				continue
			}
			bits := uint8(c - 0x2800)
			for y := 0; y < 4; y++ {
				for x := 0; x < 2; x++ {
					if bits&brailleBit[y][x] != 0 {
						dots[cy*4+y][cx*2+x] = true
					}
				}
			}
		}
	}
	f := 1
	for (pw+2*f-1)/(2*f) > w || (ph+4*f-1)/(4*f) > h {
		f++
	}
	nw, nh := (pw+f-1)/f, (ph+f-1)/f
	small := make([][]bool, nh)
	for y := range small {
		small[y] = make([]bool, nw)
		for x := range small[y] {
			set, area := 0, 0
			for dy := 0; dy < f && y*f+dy < ph; dy++ {
				for dx := 0; dx < f && x*f+dx < pw; dx++ {
					area++
					if dots[y*f+dy][x*f+dx] {
						set++
					}
				}
			}
			small[y][x] = set*2 >= area && set > 0
		}
	}
	var out []string
	for cy := 0; cy*4 < nh; cy++ {
		var b strings.Builder
		for cx := 0; cx*2 < nw; cx++ {
			var bits uint8
			for y := 0; y < 4; y++ {
				for x := 0; x < 2; x++ {
					if py, px := cy*4+y, cx*2+x; py < nh && px < nw && small[py][px] {
						bits |= brailleBit[y][x]
					}
				}
			}
			b.WriteRune(rune(0x2800 + int(bits)))
		}
		out = append(out, b.String())
	}
	return out
}

func sampleArt(g [][]rune, gw, w, h int) []string {
	nw, nh := min(gw, w), min(len(g), h)
	out := make([]string, nh)
	for y := 0; y < nh; y++ {
		src := g[y*len(g)/nh]
		var b strings.Builder
		for x := 0; x < nw; x++ {
			sx := x * gw / nw
			c := ' '
			if sx < len(src) {
				c = src[sx]
			}
			b.WriteRune(c)
		}
		out[y] = strings.TrimRight(b.String(), " ")
	}
	return out
}
