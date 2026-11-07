package tui

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	"valise/core/revive"
	"valise/core/vault"
	"valise/ui/service"
)

func TestRocketPlayer(t *testing.T) {
	t0 := time.Unix(1000, 0)
	at := func(s float64) time.Time { return t0.Add(time.Duration(s * float64(time.Second))) }

	// A landing: nothing in sight while waiting, hovers while held, lands
	// once released.
	r := newRocket(sceneLanding, t0)
	if r.pose(at(5)).show {
		t.Fatal("landing rocket in sight before go_")
	}
	r.go_(at(10), true)
	if !r.hovering(at(60)) || r.done(at(60)) {
		t.Fatal("held landing did not hover")
	}
	r.release(at(60))
	if r.done(at(61)) || !r.done(at(60+landingTo-landingHover+.1)) {
		t.Fatal("released landing timing")
	}
	// The scene keeps moving once the rocket has finished.
	end := at(100)
	a, b := r.cells(end, 60, 17), r.cells(end.Add(7*time.Second), 60, 17)
	if cellsText(a) == cellsText(b) {
		t.Fatal("background stops once the rocket part ends")
	}

	// A failure flies the rocket away; it never counts as done.
	r = newRocket(sceneLanding, t0)
	r.go_(at(10), true)
	r.fail(at(20))
	if r.done(at(100)) || r.pose(at(30)).show {
		t.Fatal("aborted rocket still in sight or done")
	}

	// A launch waits on the pad, lifts off, and can be skipped.
	r = newRocket(sceneLaunch, t0)
	if !r.pose(at(5)).show {
		t.Fatal("launch rocket not on the pad")
	}
	r.go_(at(10), false)
	if r.done(at(11)) || !r.done(at(10+launchTo-launchFrom+.1)) {
		t.Fatal("launch timing")
	}
	r = newRocket(sceneLaunch, t0)
	r.go_(at(10), false)
	r.skip(at(11))
	if !r.done(at(11)) {
		t.Fatal("skip did not finish the rocket part")
	}

	// Any window shape is covered.
	for _, sz := range [][2]int{{200, 52}, {80, 18}, {120, 26}, {60, 40}} {
		c := rocketCells(sceneLanding, sz[0], sz[1], 8, landingPose(28))
		if len(c) != sz[1] || len(c[0]) != sz[0] {
			t.Fatalf("%v: %dx%d", sz, len(c[0]), len(c))
		}
	}
}

func cellsText(c [][]sceneCell) string {
	var b strings.Builder
	for _, row := range c {
		for _, x := range row {
			b.WriteRune(x.ch)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// The landing screen while it pulls: themed caption, plain line, bar and
// scene, within the screen; plain text when the window is too small.
func TestLandingHUD(t *testing.T) {
	m, _ := world(t)
	r := newRevive("box", vault.Meta{SnapshotID: "s1", InstanceID: "inst", CreatedAt: time.Now()})
	m.stack = append(m.stack, r)
	r.step = rvPlanning
	r.startScene(m)
	r.rocket.start = time.Now().Add(-10 * time.Second) // past the fade-in
	r.prog.set(service.Progress{Stage: service.StagePull, Done: 3 << 20, Total: 8 << 20})
	for _, size := range [][2]int{{80, 24}, {120, 40}, {200, 58}} {
		m.width, m.height = size[0], size[1]
		out := screenText(m)
		for _, want := range []string{"正在进入预备轨道…", "将备份取回本机", "37%", "已用时", "遥测", "⣿"} {
			if !strings.Contains(out, want) {
				t.Fatalf("%dx%d lacks %q:\n%s", size[0], size[1], want, out)
			}
		}
		if h := lipgloss.Height(m.View().Content); h > size[1] {
			t.Fatalf("%dx%d: %d lines", size[0], size[1], h)
		}
		for i, l := range strings.Split(m.View().Content, "\n") {
			if w := lipgloss.Width(l); w > size[0] {
				t.Fatalf("%dx%d: line %d is %d wide", size[0], size[1], i, w)
			}
		}
		if testing.Verbose() {
			t.Log("\n" + out)
		}
	}
	// Below 80×24 only the notice shows; the landing goes on underneath.
	m.width, m.height = 58, 20
	if out := screenText(m); !strings.Contains(out, "窗口太小") || strings.Contains(out, "遥测") {
		t.Fatalf("small window:\n%s", out)
	}
	if !r.animating(m) {
		t.Fatal("not animating while planning")
	}
}

// Success shows only once the rocket has landed; enter skips the rest of
// the animation, and only after the work has finished.
func TestLandingRevealAfterTouchdown(t *testing.T) {
	m, _ := world(t)
	m.width, m.height = 120, 40
	r := newRevive("box", vault.Meta{SnapshotID: "s1", InstanceID: "inst", CreatedAt: time.Now()})
	m.stack = append(m.stack, r)
	r.step = rvSwapping
	r.startScene(m)
	r.rocket.go_(m.now(), true)
	if _, ok := r.key(m, "enter"); ok || strings.Contains(m.top().help(), "跳过动画") {
		t.Fatal("skip offered while the swap runs")
	}
	r.update(m, reviveFinished{res: &revive.SwapResult{State: revive.StateHealthy}})
	out := screenText(m)
	if strings.Contains(out, "登陆成功") || !strings.Contains(out, "即将完成…") {
		t.Fatalf("success shown before touchdown:\n%s", out)
	}
	if !strings.Contains(m.top().help(), "跳过动画") {
		t.Fatal("no skip once finished")
	}
	r.key(m, "enter")
	if out := screenText(m); !strings.Contains(out, "恢复成功！") {
		t.Fatalf("skip did not reveal:\n%s", out)
	}
	// A rolled-back landing shows at once.
	r.result, r.rocket = &revive.SwapResult{State: revive.StateRolledBack, Message: "健康检查失败"}, newRocket(sceneLanding, m.now())
	if out := screenText(m); !strings.Contains(out, "恢复中止") {
		t.Fatalf("rollback not shown:\n%s", out)
	}
}
