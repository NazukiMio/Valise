package tui

import (
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

// rocketPlayer times a rocket scene: the ambient clock from creation, the
// rocket's part from go_ (the operation succeeded).
type rocketPlayer struct {
	scene rocketScene
	start time.Time
	gone  time.Time // when the rocket part started; zero while waiting
}

// Speeds and end points of the rocket part, in the pieces' seconds; each
// ends once the dust has cleared. The ambient clock keeps running until the
// screen is left.
const (
	landingFrom, landingTo, landingSpeed = 3.0, 28.0, 3.2
	launchFrom, launchTo, launchSpeed    = 4.0, 25.0, 2.4
)

func newRocket(sc rocketScene, now time.Time) *rocketPlayer {
	return &rocketPlayer{scene: sc, start: now}
}

// go_ plays the rocket part (landing: appears and touches down; launch:
// ignites and lifts off).
func (r *rocketPlayer) go_(now time.Time) {
	if r.gone.IsZero() {
		r.gone = now
	}
}

func (r *rocketPlayer) phase(now time.Time) (float64, bool) {
	from, to, speed := landingFrom, landingTo, landingSpeed
	if r.scene == sceneLaunch {
		from, to, speed = launchFrom, launchTo, launchSpeed
	}
	if r.gone.IsZero() {
		return 0, false
	}
	return min(to, from+now.Sub(r.gone).Seconds()*speed), true
}

// done reports that the rocket part has finished.
func (r *rocketPlayer) done(now time.Time) bool {
	ph, ok := r.phase(now)
	return ok && ph >= map[rocketScene]float64{sceneLanding: landingTo, sceneLaunch: launchTo}[r.scene]
}

// view is the frame for cols × rows cells, "" when there is no room.
func (r *rocketPlayer) view(now time.Time, cols, rows int) string {
	w, h, ok := rocketSize(cols, rows)
	if !ok {
		return ""
	}
	amb := now.Sub(r.start).Seconds()
	ph, going := r.phase(now)
	// Waiting: a landing has no rocket in sight yet; a launch has it on
	// the pad, engines off.
	show := going || r.scene == sceneLaunch
	return rocketView(r.scene, w, h, amb, ph, show)
}
