package tui

import (
	"image/color"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"valise/ui/service"
)

// The telemetry panels drawn over a rocket scene (docs/UI.md section 8):
// a panel darkens the scene under it, which keeps moving, as if through
// tinted glass; text is laid on top and its blanks stay see-through.

var (
	hudPanelBg = lipgloss.Color("#0f1d3d")
	hudCorner  = lipgloss.NewStyle().Foreground(lipgloss.Color("#6f98b8"))
	hudEdge    = lipgloss.NewStyle().Foreground(lipgloss.Color("#24375c"))
	hudTitle   = lipgloss.NewStyle().Foreground(accent).Background(hudPanelBg).Bold(true)
	hudBig     = lipgloss.NewStyle().Foreground(accent).Bold(true)
	hudGood    = lipgloss.NewStyle().Foreground(cOK).Bold(true)
	hudBad     = lipgloss.NewStyle().Foreground(cBad).Bold(true)
	hudText    = lipgloss.NewStyle().Foreground(lipgloss.Color("#e6ecf5"))
	hudDim     = lipgloss.NewStyle().Foreground(lipgloss.Color("#8f9bb3"))
	hudFaint   = lipgloss.NewStyle().Foreground(lipgloss.Color("#4a5a7a"))
	hudBar     = lipgloss.NewStyle().Foreground(lipgloss.Color("#6f98b8"))
)

// hudFits reports whether the scene and panels have room; below it the
// screens fall back to plain text.
func hudFits(w, h int) bool { return w >= 60 && h >= 16 }

// hudWide has room for the telemetry panel beside the main one.
func hudWide(w int) bool { return w >= 100 }

type hud struct {
	buf  *uv.Buffer
	w, h int
}

func newHUD(cells [][]sceneCell, w, h int) *hud {
	b := uv.NewBuffer(w, h)
	for y := range h {
		for x := range w {
			c := uv.Cell{Content: " ", Width: 1, Style: uv.Style{Bg: rocketGround}}
			if sc := cells[y][x]; sc.col >= 0 {
				c.Content, c.Style.Fg = string(sc.ch), rocketPalette[sc.col]
			}
			b.SetCell(x, y, &c)
		}
	}
	return &hud{b, w, h}
}

// shade mixes c into the ground: what shows through a panel.
func shade(c color.Color) color.Color {
	r, g, b, _ := c.RGBA()
	gr, gg, gb, _ := rocketGround.RGBA()
	mix := func(a, z uint32) uint8 { return uint8((z + (a-z)*3/10) >> 8) }
	return color.RGBA{mix(r, gr), mix(g, gg), mix(b, gb), 255}
}

// panel darkens x, y, w × h and frames it, with title on the top edge.
func (d *hud) panel(x, y, w, h int, title string) {
	for r := y; r < y+h; r++ {
		for c := x; c < x+w; c++ {
			cell := d.buf.CellAt(c, r)
			if cell == nil {
				continue
			}
			nc := *cell
			if nc.Style.Fg != nil {
				nc.Style.Fg = shade(nc.Style.Fg)
			}
			nc.Style.Bg = hudPanelBg
			d.buf.SetCell(c, r, &nc)
		}
	}
	edge := hudEdge.Render(strings.Repeat("─", max(0, w-4)))
	d.text(x, y, w, hudCorner.Render("┏━")+edge+hudCorner.Render("━┓"))
	d.text(x, y+h-1, w, hudCorner.Render("┗━")+edge+hudCorner.Render("━┛"))
	if title != "" {
		d.text(x+3, y, w-6, hudTitle.Render(" "+title+" "))
	}
}

// text lays a styled string from x, y, clipped to w columns; blanks stay
// see-through. It returns the number of lines drawn.
func (d *hud) text(x, y, w int, s string) int {
	lines := uv.NewStyledString(s).Lines(ansi.GraphemeWidth)
	for i, line := range lines {
		col := 0
		for _, c := range line {
			if c.Width == 0 {
				continue
			}
			if col+c.Width > w {
				break
			}
			if c.Content != " " || c.Style.Bg != nil {
				nc := c
				if nc.Style.Bg == nil {
					if under := d.buf.CellAt(x+col, y+i); under != nil {
						nc.Style.Bg = under.Style.Bg
					}
				}
				d.buf.SetCell(x+col, y+i, &nc)
			}
			col += c.Width
		}
	}
	return len(lines)
}

func (d *hud) render() string { return d.buf.Render() }

// hudSweep is a bar for work that cannot be measured: a light moving to and
// fro, or standing still while the user decides.
func hudSweep(cells int, now time.Time, still bool) string {
	b := []rune(strings.Repeat("░", cells))
	if still {
		return hudFaint.Render(string(b))
	}
	glow := []rune("▒▓███▓▒")
	span := max(1, cells-len(glow))
	t := int(now.UnixMilli()/60) % (2 * span)
	if t >= span {
		t = 2*span - t
	}
	for i, r := range glow {
		if t+i < cells {
			b[t+i] = r
		}
	}
	return hudBar.Render(string(b))
}

// hudMeter is the bottom panel's bar: measured when total is known.
func hudMeter(p service.Progress, cells int, now time.Time, still bool) string {
	if p.Total <= 0 {
		return hudSweep(cells, now, still)
	}
	done := min(p.Done, p.Total)
	n := int(float64(done) / float64(p.Total) * float64(cells))
	return okStyle.Render(strings.Repeat("█", n)) + hudFaint.Render(strings.Repeat("░", cells-n)) +
		hudText.Bold(true).Render("  "+itoa(int(float64(done)/float64(p.Total)*100))+"%")
}

// hudRows renders label/value rows; values wrap within width.
func hudRows(rows [][2]string, width int) string {
	var out []string
	for _, r := range rows {
		v := lipgloss.NewStyle().Width(max(10, width-10)).Render(hudText.Render(r[1]))
		out = append(out, lipgloss.JoinHorizontal(lipgloss.Top, hudDim.Render(padRight(r[0], 10)), v))
	}
	return strings.Join(out, "\n")
}

// hudSteps lists the themed steps: done ticked, the current spinning.
func hudSteps(names []string, cur int, now time.Time) string {
	var out []string
	for i, n := range names {
		switch {
		case i < cur:
			out = append(out, okStyle.Render("✓ ")+hudDim.Render(n))
		case i == cur:
			out = append(out, accStyle.Render(spin(now)+" ")+hudText.Bold(true).Render(n))
		default:
			out = append(out, hudFaint.Render("· "+n))
		}
	}
	return strings.Join(out, "\n")
}

// rate and remaining time of a measured stage.
func eta(p service.Progress, since time.Duration) (speed, left string) {
	secs := since.Seconds()
	if p.Total <= 0 || p.Done <= 0 || secs < 1 {
		return "", ""
	}
	r := float64(p.Done) / secs
	speed = size(int64(r)) + "/s"
	if rest := float64(p.Total-p.Done) / r; rest >= 0 {
		left = "约 " + clock(time.Duration(rest*float64(time.Second)))
	}
	return speed, left
}

// hudLayout is what a screen puts on the scene.
type hudLayout struct {
	title, caption, plain string
	captionStyle          lipgloss.Style
	body                  string      // under the caption (steps, plan, result)
	telemetry             [][2]string // right panel, or a line in the main one when narrow
	bottom                string      // bottom panel label; "" hides the panel
	meter                 string      // bottom panel bar
	amount                string      // bottom panel, right
}

// drawHUD composes the scene and panels for a w × h body.
func drawHUD(cells [][]sceneCell, w, h int, l hudLayout) string {
	d := newHUD(cells, w, h)
	wide := hudWide(w)
	mainW := min(w-4, 56)
	if !wide {
		mainW = w - 4
	}
	body := l.body
	if !wide && len(l.telemetry) > 0 {
		var parts []string
		for _, r := range l.telemetry {
			parts = append(parts, r[0]+" "+r[1])
		}
		body = strings.TrimRight(body+"\n\n"+hudDim.Render(strings.Join(parts, " · ")), "\n")
	}
	bottomH := 0
	if l.bottom != "" {
		bottomH = 4
		if h < 20 {
			bottomH = 3
		}
	}
	room := h - bottomH - 2
	content := l.captionStyle.Render(l.caption)
	if l.plain != "" {
		content += "\n" + hudDim.Render(l.plain)
	}
	if body != "" {
		content += "\n\n" + body
	}
	content = lipgloss.NewStyle().Width(mainW - 6).Render(content)
	mainH := min(room, lipgloss.Height(content)+3)
	d.panel(2, 1, mainW, mainH, l.title)
	d.text(5, 2, mainW-6, clip(content, mainH-3))
	if wide && len(l.telemetry) > 0 {
		tw := 36
		rows := hudRows(l.telemetry, tw-6)
		d.panel(w-tw-2, 1, tw, lipgloss.Height(rows)+2, "遥测")
		d.text(w-tw+1, 2, tw-6, rows)
	}
	if bottomH > 0 {
		y := h - bottomH - 1
		d.panel(2, y, w-4, bottomH+1, "进度")
		d.text(5, y+1, w-10, hudText.Bold(true).Render(l.bottom))
		if l.amount != "" {
			d.text(w-6-lipgloss.Width(l.amount), y+1, lipgloss.Width(l.amount), hudDim.Render(l.amount))
		}
		d.text(5, y+bottomH-1, w-10, l.meter)
	}
	return d.render()
}

// upTo is now, or end once the work has ended (elapsed time stops there).
func upTo(now, end time.Time) time.Time {
	if end.IsZero() {
		return now
	}
	return end
}
