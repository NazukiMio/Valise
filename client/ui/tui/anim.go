package tui

import (
	"math"
	"time"

	tea "charm.land/bubbletea/v2"
)

// Animation: one frame tick for the screen on top while it animates. The
// tick stops by itself when that screen stops (or another is pushed) and
// starts again when an animating screen is back on top.

const animFrame = time.Second / 15

type animTick struct{}

// animator is a screen with a running animation.
type animator interface{ animating(m *Model) bool }

func (m *Model) ensureAnim() tea.Cmd {
	if m.animOn {
		return nil
	}
	if a, ok := m.top().(animator); !ok || !a.animating(m) {
		return nil
	}
	m.animOn = true
	return tea.Tick(animFrame, func(time.Time) tea.Msg { return animTick{} })
}

// rocketPlayer times a rocket scene (docs/UI.md section 8): the ambient
// clock runs from creation until the screen is left; the rocket's part
// starts with go_, at the pieces' own speed. A landing may hover above the
// bottom panel until release (the work has not finished yet) and flies
// away on abort.
type rocketPlayer struct {
	scene  rocketScene
	start  time.Time
	going  bool
	anchor time.Time // phase = base + seconds since anchor
	base   float64
	hold   float64 // hover at this phase until release; 0 when not held
	abort  time.Time
	from   rocketPose // where the rocket was when aborted
}

// Phases of the rocket's part, in the pieces' seconds. Each ends once the
// dust has cleared; the landing hovers at landingHover (above the bottom
// panel) while it waits.
const (
	landingFrom, landingHover, landingTo = 3.0, 13.0, 28.0
	launchFrom, launchTo                 = 4.0, 25.0
	abortRise                            = 60.0 // units per second², upwards
)

func newRocket(sc rocketScene, now time.Time) *rocketPlayer {
	return &rocketPlayer{scene: sc, start: now}
}

// go_ starts the rocket's part (landing: comes into sight and comes down;
// launch: ignites and lifts off). held keeps a landing hovering until
// release.
func (r *rocketPlayer) go_(now time.Time, held bool) {
	if r.going {
		return
	}
	r.going, r.anchor, r.base = true, now, landingFrom
	if r.scene == sceneLaunch {
		r.base = launchFrom
	}
	if held && r.scene == sceneLanding {
		r.hold = landingHover
	}
}

// release lets a hovering landing touch down.
func (r *rocketPlayer) release(now time.Time) {
	if r.hold > 0 {
		r.base, r.anchor, r.hold = r.raw(now), now, 0
	}
}

// fail sends a rocket on its way down back up and out of sight; a rocket
// not yet in sight stays away.
func (r *rocketPlayer) fail(now time.Time) {
	if r.going && r.abort.IsZero() && !r.done(now) {
		r.from, r.abort = r.pose(now), now
	}
}

// skip jumps to the end of the rocket's part.
func (r *rocketPlayer) skip(now time.Time) {
	r.going, r.hold, r.anchor, r.base = true, 0, now, r.end()
}

func (r *rocketPlayer) end() float64 {
	if r.scene == sceneLaunch {
		return launchTo
	}
	return landingTo
}

func (r *rocketPlayer) raw(now time.Time) float64 {
	ph := min(r.end(), r.base+now.Sub(r.anchor).Seconds())
	if r.hold > 0 {
		ph = min(ph, r.hold)
	}
	return ph
}

// phase is the rocket's part's clock; ok is false before go_.
func (r *rocketPlayer) phase(now time.Time) (float64, bool) {
	if !r.going {
		return 0, false
	}
	return r.raw(now), true
}

// hovering reports a landing waiting above the ground.
func (r *rocketPlayer) hovering(now time.Time) bool {
	return r.hold > 0 && r.raw(now) >= r.hold
}

// done reports that the rocket's part has finished (or was skipped).
func (r *rocketPlayer) done(now time.Time) bool {
	ph, ok := r.phase(now)
	return ok && r.abort.IsZero() && ph >= r.end()
}

func (r *rocketPlayer) pose(now time.Time) rocketPose {
	amb := now.Sub(r.start).Seconds()
	if !r.abort.IsZero() {
		t := now.Sub(r.abort).Seconds()
		ps := r.from
		ps.cy -= abortRise * t * t
		ps.p, ps.ph = 0, 2*amb // full flame, flickering; no dust
		ps.show = ps.cy+70 > 0
		return ps
	}
	ph, going := r.phase(now)
	switch {
	case r.scene == sceneLaunch:
		// Waiting: on the pad, engines off.
		return launchPose(ph)
	case !going:
		// Waiting: a landing has no rocket in sight yet.
		return rocketPose{}
	case r.hovering(now):
		// Hovering: a slight bob keeps the flame alive.
		return landingPose(ph + .25*math.Sin(amb*1.6))
	}
	return landingPose(ph)
}

// cells is the frame for w × h cells.
func (r *rocketPlayer) cells(now time.Time, w, h int) [][]sceneCell {
	return rocketCells(r.scene, w, h, now.Sub(r.start).Seconds(), r.pose(now))
}
