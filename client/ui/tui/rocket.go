package tui

// The escape pod's rocket: two braille scenes ported from the pieces in
// assets/TUI/rocket ("静海 · 着陆" and "地球 · 启航", by the Valise authors).
//
// Two clocks drive a scene: ambient (stars, planets, meteors, sea) always
// runs; the rocket's part follows a pose computed by rocketPlayer (anim.go).
// The screens draw it under their telemetry panels (hud.go).

import (
	"image/color"
	"math"

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
	rocketPalette = func() []color.Color {
		var out []color.Color
		for _, c := range []string{"#172b50", "#385477", "#6f98b8", "#b6d9df", "#e9e7d7", "#b9534c", "#f19872", "#a775aa", "#d4ae83", "#78644f", "#fff2bd", "#75a6be"} {
			out = append(out, lipgloss.Color(c))
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

// rocketPose places the rocket: centre, descent progress p (the landing
// flame shortens as it grows) and the phase that drives flicker and dust.
type rocketPose struct {
	show       bool
	cx, cy, p  float64
	ph         float64
	launchFire bool // the launch flame (ignition and ascent) instead of the landing one
}

// landingPose and launchPose are the pieces' own rocket paths.
func landingPose(ph float64) rocketPose {
	p := min(1, max(0, ph-3)/18)
	j := 0.0
	if p > .75 && p < 1 {
		j = math.Sin((p - .75) / .25 * math.Pi)
	}
	return rocketPose{show: true, p: p, ph: ph,
		cy: -65 + 143*(1-(1-p)*(1-p)) + j*.7*math.Sin(ph*19), cx: 122 + j*1.3*math.Sin(ph*15)}
}

func launchPose(ph float64) rocketPose {
	p := min(1, max(0, ph-7)/13)
	j := 0.0
	if ph > 4 && ph < 10 {
		j = math.Sin((ph - 4) / 6 * math.Pi)
	}
	return rocketPose{show: true, p: p, ph: ph, launchFire: true,
		cy: 78 - 230*p*p + j*.45*math.Sin(ph*24), cx: 122 + j*.6*math.Sin(ph*21)}
}

// sceneCell is one braille cell: its rune and palette index (-1: empty).
type sceneCell struct {
	ch  rune
	col int
}

// rocketCells renders w × h cells, scaled uniformly to cover the area and
// cropped around the centre.
func rocketCells(sc rocketScene, w, h int, ambient float64, ps rocketPose) [][]sceneCell {
	k := max(float64(w)/rocketCols, float64(h)/rocketRows)
	W, H := int(math.Ceil(rocketCols*k)), int(math.Ceil(rocketRows*k))
	ox, oy := (W-w)/2, (H-h)/2
	sx, sy := float64(rocketCols)/float64(W), float64(rocketRows)/float64(H)
	out := make([][]sceneCell, h)
	for r := range out {
		out[r] = make([]sceneCell, w)
		for c := range out[r] {
			vc, vr := c+ox, r+oy
			bits, col := 0, -1
			for dy := 0; dy < 4; dy++ {
				for dx := 0; dx < 2; dx++ {
					x, y := float64(vc*2+dx)*sx, float64(vr*4+dy)*sy*1.2
					on, kk := rocketDot(sc, vc, vr, x, y, ambient, ps)
					if on && (ambient >= 3 || rhash(float64(vc), float64(vr+731)) < max(0, (ambient-.2)/2.8)) {
						bits |= int(brailleBit[dy][dx])
						col = kk
					}
				}
			}
			out[r][c] = sceneCell{rune(0x2800 + bits), col}
			if bits == 0 {
				out[r][c] = sceneCell{' ', -1}
			}
		}
	}
	return out
}

// rocketDot is one braille dot of the scene: whether it is lit and its
// palette index. x, y are in the pieces' coordinates.
func rocketDot(sc rocketScene, c, r int, x, y, t float64, ps rocketPose) (on bool, k int) {
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
	if !ps.show {
		return on, k
	}
	ph, p := ps.ph, ps.p
	X, Y := x-ps.cx, y-ps.cy
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
	if !ps.launchFire {
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
