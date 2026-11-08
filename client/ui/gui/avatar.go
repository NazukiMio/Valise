package gui

import (
	"github.com/egoist/mygo/ui"

	"valise/core/profile"
	"valise/ui/iconart"
)

// avatar shows the harness's own character art in its color on a faint
// tint of it, so agents of one harness group at a glance (docs/UI.md 7.2).
// A harness without art gets the built-in robot in the palette's brown.
//
// The art is painted as dots, not set as text: fonts differ in which of
// these characters they have (braille is often missing), and dots read the
// same everywhere. Screen readers hear only the label.
func avatar(c *ui.Context, d *profile.Display, side float32, large bool) {
	art := iconart.Compact(d)
	if large {
		art = iconart.Standard(d)
	}
	ink := ui.Hex("#8c7e6c")
	if iconart.Own(d) && d.Color != "" {
		ink = ui.Hex(d.Color)
	}
	label := "Agent 图标"
	if iconart.Own(d) {
		label = "harness 图标"
	}
	grid := dotGrid(art)
	ui.Box(c).Size(side, side).Radius(side/4).Background(ink.Alpha(0.10)).Border(1, ink.Alpha(0.18)).
		Label(label).Draw(func(p *ui.Painter, r ui.Rect) {
		rows, cols := len(grid), 0
		for _, g := range grid {
			cols = max(cols, len(g))
		}
		if rows == 0 || cols == 0 {
			return
		}
		pitch := min((r.W-side*0.24)/float32(cols), (r.H-side*0.24)/float32(rows))
		x0 := r.X + (r.W-pitch*float32(cols))/2
		y0 := r.Y + (r.H-pitch*float32(rows))/2
		for y, g := range grid {
			for x, v := range g {
				if v > 0 {
					p.FillPath((&ui.Path{}).Circle(x0+(float32(x)+0.5)*pitch, y0+(float32(y)+0.5)*pitch, pitch*0.42*v), ink)
				}
			}
		}
	})
}

// dotGrid turns character art into dots with a size from 0 to 1: each
// braille cell becomes its 2×4 dots; other characters are one cell of 2×4
// whose dots follow the character's look (● large, • medium, : and . small).
func dotGrid(art []string) [][]float32 {
	var out [][]float32
	for _, line := range art {
		rs := []rune(line)
		block := make([][]float32, 4)
		for i := range block {
			block[i] = make([]float32, 2*len(rs))
		}
		for cx, ch := range rs {
			set := func(x, y int, v float32) { block[y][cx*2+x] = v }
			switch {
			case ch >= 0x2800 && ch <= 0x28FF:
				bits := uint8(ch - 0x2800)
				for y := 0; y < 4; y++ {
					for x := 0; x < 2; x++ {
						if bits&iconart.BrailleBit[y][x] != 0 {
							set(x, y, 1)
						}
					}
				}
			case ch == ' ':
			case ch == '●':
				set(0, 1, 1.15)
				set(1, 1, 1.15)
				set(0, 2, 1.15)
				set(1, 2, 1.15)
			case ch == '•':
				set(0, 1, 0.9)
				set(1, 2, 0.9)
			case ch == ':':
				set(0, 1, 0.6)
				set(0, 3, 0.6)
			case ch == '.':
				set(0, 3, 0.6)
			default:
				set(0, 2, 0.8)
			}
		}
		out = append(out, block...)
	}
	return out
}
