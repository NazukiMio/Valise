package tui

// The escape pod being built: "头罩合装" (assets/TUI/rocket/assembly-quiet,
// by the Valise authors), ported from its scene script. Six welding arms
// work on the hull while the nose cone comes down.
//
// Two clocks: motion (tm) always runs and moves the arms and sparks; the
// pose (tp) follows the work: the cone comes down and the arms keep
// welding (tp held at assemblyHold) until the seal is done, then the arms
// stop one by one and withdraw, a little faster than the piece.

import (
	"image/color"
	"math"
	"time"

	"charm.land/lipgloss/v2"
)

const (
	assemblyHold  = 24.0 // the pose held while the work runs
	assemblyEnd   = 36.0 // every arm has withdrawn
	assemblyFast  = 3.0  // pose speed once the work is done
	assemblySpark = 35.5 // the sparks' schedule repeats
	asmW, asmH    = 240, 136
)

// assemblyBase is where the assembly palette starts in rocketPalette.
var assemblyBase = func() int {
	n := len(rocketPalette)
	for _, c := range []string{"#263751", "#61738a", "#b8d3db", "#eee5d5", "#e78165", "#f4bd59", "#66b6aa", "#ef625c"} {
		rocketPalette = append(rocketPalette, color.Color(lipgloss.Color(c)))
	}
	return n
}()

// assemblyPlayer times the scene for one seal.
type assemblyPlayer struct {
	start  time.Time
	doneAt time.Time // zero while the work runs
	tpDone float64   // the pose when the work finished
}

func newAssembly(now time.Time) *assemblyPlayer { return &assemblyPlayer{start: now} }

// finish starts the arms' withdrawal.
func (a *assemblyPlayer) finish(now time.Time) {
	if a.doneAt.IsZero() {
		a.tpDone, a.doneAt = a.pose(now), now
	}
}

func (a *assemblyPlayer) pose(now time.Time) float64 {
	if a.doneAt.IsZero() {
		return min(now.Sub(a.start).Seconds(), assemblyHold)
	}
	return min(assemblyEnd, a.tpDone+now.Sub(a.doneAt).Seconds()*assemblyFast)
}

// over: the arms have withdrawn.
func (a *assemblyPlayer) over(now time.Time) bool {
	return !a.doneAt.IsZero() && a.pose(now) >= assemblyEnd
}

func (a *assemblyPlayer) cells(now time.Time, w, h int) [][]sceneCell {
	pix := assemblyPixels(now.Sub(a.start).Seconds(), a.pose(now))
	k := max(float64(w)/120, float64(h)/34)
	W, H := int(math.Ceil(120*k)), int(math.Ceil(34*k))
	ox, oy := (W-w)/2, (H-h)/2
	out := make([][]sceneCell, h)
	for r := range out {
		out[r] = make([]sceneCell, w)
		for c := range out[r] {
			bits, col := 0, -1
			for dy := 0; dy < 4; dy++ {
				for dx := 0; dx < 2; dx++ {
					x := int(float64((c+ox)*2+dx) * 120 / float64(W))
					y := int(float64((r+oy)*4+dy) * 34 / float64(H))
					if x >= 0 && x < asmW && y >= 0 && y < asmH {
						if v := pix[y*asmW+x]; v >= 0 {
							bits |= int(brailleBit[dy][dx])
							col = assemblyBase + int(v)
						}
					}
				}
			}
			out[r][c] = sceneCell{' ', -1}
			if bits != 0 {
				out[r][c] = sceneCell{rune(0x2800 + bits), col}
			}
		}
	}
	return out
}

// smootherstep from a to b.
func asmE(a, b, t float64) float64 {
	q := max(0, min(1, (t-a)/(b-a)))
	return q * q * q * (q*(q*6-15) + 10)
}

func blend(a, b, q float64) float64 { return a + (b-a)*q }

// odd is JavaScript's n % 2 != 0 for an integer-valued float.
func odd(n float64) bool { return math.Mod(math.Abs(n), 2) == 1 }

// assemblyPixels draws one frame at motion time tm and pose tp.
func assemblyPixels(tm, tp float64) []int8 {
	pix := make([]int8, asmW*asmH)
	for i := range pix {
		pix[i] = -1
	}
	point := func(x, y float64, k int8) {
		xi, yi := int(math.Round(x)), int(math.Round(y))
		if xi >= 0 && xi < asmW && yi >= 0 && yi < asmH {
			pix[yi*asmW+xi] = k
		}
	}
	line := func(x, y, a, b float64, k int8) {
		n := math.Ceil(max(math.Abs(a-x), math.Abs(b-y)))
		d := n
		if d == 0 {
			d = 1
		}
		for i := 0.0; i <= n; i++ {
			point(blend(x, a, i/d), blend(y, b, i/d), k)
		}
	}
	box := func(x, y, w, h float64, k int8) {
		for j := 0.0; j < h; j++ {
			line(x, y+j, x+w, y+j, k)
		}
	}
	disk := func(x, y, r float64, k int8) {
		for j := -r; j <= r; j++ {
			s := math.Sqrt(r*r - j*j)
			line(x-s, y+j, x+s, y+j, k)
		}
	}
	// The hall: trusses, rails, shelves, the engine stand.
	for x := 10.0; x < 240; x += 44 {
		line(x, 18, x, 120, 0)
		line(x, 18, x+22, 4, 0)
		line(x+22, 4, x+44, 18, 0)
	}
	line(0, 18, 239, 18, 1)
	line(0, 23, 239, 23, 0)
	line(0, 121, 239, 121, 1)
	for x := 23.0; x < 240; x += 65 {
		box(x, 28, 18, 1, 2)
	}
	for y := 83.0; y < 120; y += 17 {
		line(9, y, 44, y, 1)
		box(13, y-9, 10, 8, 0)
		box(29, y-7, 12, 6, 0)
	}
	box(102, 118, 38, 3, 1)
	box(199, 98, 19, 21, 0)
	box(114, 111, 14, 4, 1)
	line(116, 115, 113, 118, 2)
	line(126, 115, 129, 118, 2)
	line(113, 118, 129, 118, 2)
	// The checkerboard hull, porthole and fins.
	check := func(a, x float64) int8 {
		if odd(math.Floor(a/11) + math.Floor((x-108)/12)) {
			return 4
		}
		return 3
	}
	for y := 59.0; y < 111; y++ {
		w := 13 - (y-83)*.19
		if y < 83 {
			w = 13 - math.Pow((y-70)/40, 2)
		}
		for x := math.Ceil(121 - w); x <= 121+w; x++ {
			point(x, y, check(y-59, x))
		}
	}
	disk(117, 76, 7, 4)
	disk(117, 76, 5, 2)
	disk(117, 76, 4, 0)
	line(114, 73, 116, 72, 3)
	for _, side := range []float64{-1, 1} {
		for j := 0.0; j < 25; j++ {
			inner, outer := 11+j*.19, 13+10*math.Sin(j/25*math.Pi*.65)
			line(121+side*inner, 88+j, 121+side*outer, 88+j, 4)
		}
	}
	// The nose cone comes down.
	capY := blend(-32, 36, asmE(5, 17, tp))
	for j := 0.0; j < 23; j++ {
		w := 13 * math.Sqrt(max(0, 1-math.Pow((j-23)/23, 2)))
		for x := math.Ceil(121 - w); x <= 121+w; x++ {
			k := int8(4)
			if j >= 10 {
				k = check(j-10, x)
			}
			point(x, capY+j, k)
		}
	}
	line(135, 78, 137, 79, 2)
	line(137, 79, 137, 89, 2)
	line(137, 89, 134, 94, 2)
	// Six arms: welding along the hull, the top two move to the seam once
	// the cone is down, then each stops and withdraws.
	stops := []float64{0, 3, 6, 1.5, 4.5, 7.5}
	for i := 0; i < 6; i++ {
		fi, side, n := float64(i), -1.0, float64(i%3)
		if i >= 3 {
			side = 1
		}
		baseX, baseY := 121+side*39, 72+n*20
		stop := 25 + stops[i]
		gather := 0.0
		if i%3 == 0 {
			gather = asmE(17, 20, tp)
		}
		withdraw := asmE(stop, stop+3, tp)
		workingY := 75 + n*14 + 7*math.Sin(tm*(.47+n*.11)+fi*2.1) + 3*math.Sin(tm*.83+fi)
		seamY := 59 + 1.8*math.Sin(tm*.83+fi*1.7)
		y := blend(workingY, seamY, gather)
		x := 121 + side*(13+1.1*math.Sin(tm*.7+fi))
		x, y = blend(x, baseX-side*4, withdraw), blend(y, baseY-4, withdraw)
		ex, ey := baseX-side*(8+2*math.Sin(tm*.35+fi)), blend(baseY-9, y+5, .36)
		box(baseX-3, baseY-2, 6, 4, 1)
		line(baseX, baseY, ex, ey, 1)
		disk(ex, ey, 2, 2)
		line(ex, ey, x, y, 2)
		disk(x, y, 1, 5)
		line(x, y, x-side*2, y+1, 1)
	}
	// The crane hook lifts away once the cone is set.
	hy := capY - 5 - 30*asmE(19, 23, tp)
	box(113, 18, 16, 5, 1)
	if hy > 24 {
		line(121, 24, 121, hy, 1)
	}
	// Sparks: an irregular schedule per arm, each burst ballistic.
	ts := math.Mod(tm, assemblySpark)
	for i := 0; i < 6; i++ {
		fi, side, n := float64(i), -1.0, float64(i%3)
		if i >= 3 {
			side = 1
		}
		stop := 25 + stops[i]
		for e := 0; e < 34; e++ {
			fe := float64(e)
			birth := .2 + fe*1.04 + rhash(fi*67+fe, 31)*.92
			age := ts - birth
			if age < 0 || age > 2.7 {
				continue
			}
			tpb, tmb := tp-age, tm-age // the pose and motion at birth
			if (i%3 == 0 && tpb > 16 && tpb < 20) || tpb > stop {
				continue
			}
			gather := 0.0
			if i%3 == 0 {
				gather = asmE(17, 20, tpb)
			}
			sy := blend(75+n*14+7*math.Sin(tmb*(.47+n*.11)+fi*2.1)+3*math.Sin(tmb*.83+fi), 59+1.8*math.Sin(tmb*.83+fi*1.7), gather)
			sx := 121 + side*(13+1.1*math.Sin(tmb*.7+fi))
			for p := 0.0; p < 4; p++ {
				if age > 1.3+rhash(fe*4+p, fi+71)*1.4 {
					continue
				}
				vx, vy := side*(3+rhash(fe*7+p, fi+11)*11), -5-rhash(p+fe*3, fi+9)*10
				px, py := sx+vx*age, sy+vy*age+16*age*age
				if py > 120 {
					continue
				}
				k := int8(4)
				switch {
				case age < .16:
					k = 3
				case age < .8:
					k = 5
				}
				point(px, py, k)
				if age > .2 && age < 1.7 {
					line(px-vx*.035, py-(vy+32*age)*.035, px, py, k)
				}
			}
		}
	}
	return pix
}
