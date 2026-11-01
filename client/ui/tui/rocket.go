package tui

// The escape pod's rocket: two braille scenes ported from the pieces in
// assets/TUI/rocket ("静海 · 着陆" and "地球 · 启航"). Their runtime and
// hash() follow ascii-motion and ascii.rest:
//
//   MIT License, Copyright (c) 2026 ascii-motion contributors;
//   Copyright (c) 2026 bas3line (https://github.com/bas3line/ascii).
//   Permission is hereby granted, free of charge, to any person obtaining a
//   copy of this software and associated documentation files (the
//   "Software"), to deal in the Software without restriction, including
//   without limitation the rights to use, copy, modify, merge, publish,
//   distribute, sublicense, and/or sell copies of the Software, and to
//   permit persons to whom the Software is furnished to do so, subject to
//   the following conditions: The above copyright notice and this
//   permission notice shall be included in all copies or substantial
//   portions of the Software. THE SOFTWARE IS PROVIDED "AS IS", WITHOUT
//   WARRANTY OF ANY KIND.
//
// Two clocks drive a scene: ambient (stars, planets, meteors, sea) always
// runs; phase moves the rocket. A long operation shows the scene with the
// rocket waiting (launch: on the pad; landing: not yet in sight); when it
// succeeds the rocket part plays.

import (
	"math"
	"strings"

	"charm.land/lipgloss/v2"
)

type rocketScene int

const (
	sceneLanding rocketScene = iota
	sceneLaunch
)

// Size of the original pieces, in cells.
const rocketCols, rocketRows = 120, 34

var (
	rocketGround  = lipgloss.Color("#080e24")
	rocketPalette = []string{"#172b50", "#385477", "#6f98b8", "#b6d9df", "#e9e7d7", "#b9534c", "#f19872", "#a775aa", "#d4ae83", "#78644f", "#fff2bd", "#75a6be"}
	rocketStyles  = func() []lipgloss.Style {
		out := make([]lipgloss.Style, len(rocketPalette))
		for i, c := range rocketPalette {
			out[i] = lipgloss.NewStyle().Foreground(lipgloss.Color(c)).Background(rocketGround)
		}
		return out
	}()
)

// rhash is ascii.rest's hash(): JavaScript int32 arithmetic on uint32.
func rhash(x, y float64) float64 {
	h := uint32(int32(x))*374761393 + uint32(int32(y))*668265263
	h = (h ^ (h >> 13)) * 1274126177
	return float64(h^(h>>16)) / 4294967296
}

// rocketSize keeps the pieces' aspect within cols × rows; ok is false when
// the space is too small to read.
func rocketSize(cols, rows int) (w, h int, ok bool) {
	k := min(float64(cols)/rocketCols, float64(rows)/rocketRows, 1)
	w, h = int(rocketCols*k+0.5), int(rocketRows*k+0.5)
	return w, h, h >= 6 && w >= 24
}

// rocketView renders one frame of w × h cells. rocket hides the rocket
// (and its flame and dust) when false.
func rocketView(sc rocketScene, w, h int, ambient, phase float64, rocket bool) string {
	sx, sy := float64(rocketCols)/float64(w), float64(rocketRows)/float64(h)
	var p, j, cx, cy float64
	if sc == sceneLanding {
		p = min(1, max(0, phase-3)/18)
		if p > .75 && p < 1 {
			j = math.Sin((p - .75) / .25 * math.Pi)
		}
		cy = -65 + 143*(1-(1-p)*(1-p)) + j*.7*math.Sin(phase*19)
		cx = 122 + j*1.3*math.Sin(phase*15)
	} else {
		p = min(1, max(0, phase-7)/13)
		if phase > 4 && phase < 10 {
			j = math.Sin((phase - 4) / 6 * math.Pi)
		}
		cy = 78 - 230*p*p + j*.45*math.Sin(phase*24)
		cx = 122 + j*.6*math.Sin(phase*21)
	}
	var b strings.Builder
	for r := 0; r < h; r++ {
		run, runCol := []rune{}, -1
		flush := func() {
			if len(run) > 0 {
				b.WriteString(rocketStyles[max(runCol, 0)].Render(string(run)))
			}
			run = run[:0]
		}
		for c := 0; c < w; c++ {
			bits, col := 0, 0
			for dy := 0; dy < 4; dy++ {
				for dx := 0; dx < 2; dx++ {
					x, y := float64(c*2+dx)*sx, float64(r*4+dy)*sy*1.2
					on, k := rocketDot(sc, c, r, x, y, ambient, phase, p, cx, cy, rocket)
					if on && (ambient >= 3 || rhash(float64(c), float64(r+731)) < max(0, (ambient-.2)/2.8)) {
						bits |= int(brailleBit[dy][dx])
						col = k
					}
				}
			}
			ch := ' '
			if bits != 0 {
				ch = rune(0x2800 + bits)
			}
			if col != runCol && bits != 0 {
				flush()
				runCol = col
			}
			run = append(run, ch)
		}
		flush()
		if r < h-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// rocketDot is one braille dot of the scene: whether it is lit and its
// palette index. x, y are in the pieces' coordinates.
func rocketDot(sc rocketScene, c, r int, x, y, t, ph, p, cx, cy float64, rocket bool) (on bool, k int) {
	if rhash(float64(c), float64(r)) > .969 && y < 112 {
		on, k = true, 2
		if rhash(float64(c+91), float64(r+37)) > .92 {
			k = 1
			if math.Sin(t*1.1+rhash(float64(c), float64(r))*20) > .55 {
				k = 3
			}
		}
	}
	planets := [][4]float64{{35, 48, 21, 5}, {200, 20, 8, 11}, {195, 78, 11, 7}, {70, 19, 6, 11}}
	if sc == sceneLaunch {
		planets = [][4]float64{{198, 26, 12, 4}}
	}
	for _, pl := range planets {
		px, py, rad := pl[0], pl[1], pl[2]
		xx, yy := x-px-math.Sin(t*.18+px)*1.1, (y-py)/1.2
		if math.Hypot(xx, yy) < rad {
			on, k = rhash(x, y) > .24, int(pl[3])
			if math.Hypot(xx+rad*.35, yy+rad*.3) < rad*.25 {
				k = 0
			}
		}
		if px == 35 && math.Abs(yy*.8+xx*.35) < 1.8 && math.Abs(xx) < 40 {
			on, k = true, 8
		}
	}
	for m := 0.0; m < 2; m++ {
		age := math.Mod(t+m*8, 19) - 5
		if age >= 0 && age < 1.35 {
			headX, headY := 65+m*82+age*40, 8+age*15
			trail := (headX - x) / 40
			if trail >= 0 && trail < .48 && math.Abs(y-(headY-trail*15)) < .7 && y < 115 {
				on, k = true, 1
				switch {
				case trail < .07:
					k = 4
				case trail < .22:
					k = 3
				}
			}
		}
	}
	if sc == sceneLanding {
		if y > 140+4*math.Sin(x*.026)+2*math.Sin(x*.1) {
			on, k = rhash(x, y) > .67, 9
			for _, a := range []float64{35, 189} {
				if math.Abs((x-a)*(x-a)/500+(y-152)*(y-152)/24-1) < .15 {
					on, k = true, 8
				}
			}
		}
	} else {
		if y > 139+2*math.Sin(x*.035) {
			on, k = rhash(x, y) > .63, 9
			if x < 75 || x > 169 {
				on, k = math.Sin(y*1.7+x*.08+t*.65) > .87, 2
			}
		}
		if y > 132+5*math.Sin(x*.036) && y < 140 {
			on, k = true, 0
		}
		if y >= 140 && y < 144 && x > 95 && x < 150 {
			on, k = true, 3
		}
		if x > 88 && x < 96 && y > 86 && y < 141 {
			on, k = math.Abs(x-89) < 1 || math.Abs(x-95) < 1 || math.Abs(math.Mod(y-86, 12)-(x-88)*1.6) < 1, 2
		}
		if y > 96 && y < 98 && x >= 95 && x < 111 {
			on, k = true, 3
		}
	}
	if !rocket {
		return on, k
	}
	X, Y := x-cx, y-cy
	width := 11 - (Y-15)*.035
	if Y < 15 {
		width = math.Sqrt(max(0, 1-((Y-15)/15)*((Y-15)/15))) * 11
	}
	if Y >= 0 && Y < 51 && math.Abs(X) < width {
		on, k = true, 4
		if (int(math.Floor(Y/13))+int(math.Floor((X+12)/11)))%2 != 0 || Y < 12 {
			k = 6
		}
		if d := math.Hypot(X, Y-24); d < 7 {
			switch {
			case d > 5:
				k = 6
			case d > 4:
				k = 4
			default:
				k = 0
			}
		}
	}
	if Y > 40 && Y < 62 && math.Abs(X) > 8 && math.Abs(X) < 19-(Y-40)*.25 {
		on, k = true, 5
	}
	if Y > 50 && Y < 57 && math.Abs(X) < 7 {
		on, k = true, 9
	}
	flicker := .8 + .2*math.Sin(ph*22+y)
	if sc == sceneLanding {
		if p < 1 && Y > 57 && Y < 57+(1-p)*24+7 && math.Abs(X) < (7-(Y-57)*.19)*flicker {
			on, k = true, flameColor(Y)
		}
		if ph > 16 && ph < 28 && y > 127 && y < 147 && math.Abs(X) < (ph-16)*5+8 {
			if math.Sin(x*.14-ph*2)+math.Sin(y*.5+ph) > .4 && rhash(x, y) > .52 {
				on, k = true, 3
			}
		}
		return on, k
	}
	if ph > 4 && Y > 57 && Y < 57+min(36, (ph-4)*5) && math.Abs(X) < (7-(Y-57)*.13)*flicker {
		on, k = true, flameColor(Y)
	}
	if ph > 4 && ph < 25 && y > 127 && y < 148 && math.Abs(x-122) < min(90, (ph-4)*9) {
		fade, edge := max(0, 1-max(0, ph-14)/11), 1-math.Abs(y-139)/13
		if math.Sin(x*.14-ph*1.4)+math.Sin(y*.5+ph*.7) > .5 && rhash(x, y) > .68-fade*.23 && edge > 0 {
			on, k = true, 2
			if ph < 8 {
				k = 3
			}
		}
	}
	return on, k
}

func flameColor(Y float64) int {
	if Y < 65 {
		return 10
	}
	return 11
}
