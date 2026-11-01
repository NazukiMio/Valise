package tui

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	"valise/core/vault"
	"valise/ui/service"
)

func TestRocketFrames(t *testing.T) {
	t0 := time.Unix(1000, 0)
	for _, sc := range []rocketScene{sceneLanding, sceneLaunch} {
		r := newRocket(sc, t0)
		wait := r.view(t0.Add(10*time.Second), 60, 17)
		if lipgloss.Height(wait) != 17 || lipgloss.Width(wait) != 60 {
			t.Fatalf("scene %d: %dx%d", sc, lipgloss.Width(wait), lipgloss.Height(wait))
		}
		if r.done(t0.Add(time.Hour)) {
			t.Fatal("done before go_")
		}
		r.go_(t0.Add(10 * time.Second))
		if r.done(t0.Add(11 * time.Second)) {
			t.Fatal("done at once")
		}
		end := t0.Add(30 * time.Second)
		if !r.done(end) {
			t.Fatal("rocket part never ends")
		}
		if a, b := r.view(end, 60, 17), r.view(end.Add(time.Minute), 60, 17); a != b {
			t.Fatal("finished frame still moves")
		}
	}
	// The landing hides the rocket while waiting: the rocket's orange
	// cells only appear after go_.
	r := newRocket(sceneLanding, t0)
	waiting := r.view(t0.Add(5*time.Second), 120, 34)
	r.go_(t0.Add(5 * time.Second))
	landed := r.view(t0.Add(time.Minute), 120, 34)
	if waiting == landed {
		t.Fatal("rocket never appeared")
	}
	if _, _, ok := rocketSize(30, 4); ok {
		t.Fatal("too small a space accepted")
	}
	if !strings.Contains(landed, "⣿") {
		t.Fatal("no solid cells in the landed frame")
	}
}

// The landing screen while it pulls: steps, a bar with the percentage and
// the scene below, all within the screen (80×24 and larger).
func TestLandingProgressView(t *testing.T) {
	m, _ := world(t)
	r := newRevive("box", vault.Meta{SnapshotID: "s1", InstanceID: "inst", CreatedAt: time.Now()})
	m.stack = append(m.stack, r)
	r.step = rvPlanning
	r.startScene(m)
	r.rocket.start = time.Now().Add(-10 * time.Second) // past the fade-in
	r.prog.set(service.Progress{Stage: service.StagePull, Done: 3 << 20, Total: 8 << 20})
	for _, size := range [][2]int{{80, 24}, {120, 40}} {
		m.width, m.height = size[0], size[1]
		out := screenText(m)
		for _, want := range []string{"取回逃生舱到这台电脑", "37%", "已用时", "在这台电脑上打开"} {
			if !strings.Contains(out, want) {
				t.Fatalf("%dx%d lacks %q:\n%s", size[0], size[1], want, out)
			}
		}
		if h := lipgloss.Height(m.View().Content); h > size[1] {
			t.Fatalf("%dx%d: %d lines", size[0], size[1], h)
		}
		if !strings.Contains(out, "⣿") {
			t.Fatalf("no scene at %dx%d:\n%s", size[0], size[1], out)
		}
		if testing.Verbose() {
			t.Log("\n" + out)
		}
	}
	if !r.animating(m) {
		t.Fatal("not animating while planning")
	}
}
