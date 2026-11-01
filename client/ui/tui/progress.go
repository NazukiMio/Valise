package tui

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"valise/ui/service"
)

// progressBox receives a long operation's progress from its goroutine;
// the animation tick reads it, so reports never queue up as messages.
type progressBox struct {
	mu    sync.Mutex
	cur   service.Progress
	since time.Time // when the current stage started
}

func (b *progressBox) set(p service.Progress) {
	b.mu.Lock()
	if p.Stage != b.cur.Stage || b.since.IsZero() {
		b.since = time.Now()
	}
	b.cur = p
	b.mu.Unlock()
}

// stageTime is how long the current stage has run.
func (b *progressBox) stageTime() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.since.IsZero() {
		return 0
	}
	return time.Since(b.since)
}

func (b *progressBox) get() service.Progress {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cur
}

// step is one line of a progress list.
type step struct{ stage, label string }

var spinner = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")

func spin(now time.Time) string {
	return string(spinner[int(now.UnixMilli()/80)%len(spinner)])
}

// stepsView lists steps: done ones ticked, the current one spinning with a
// bar when it can be measured, later ones dim. A stage not in steps (or
// none yet) counts as the first.
func stepsView(steps []step, p service.Progress, now, began time.Time) string {
	cur := 0
	for i, s := range steps {
		if s.stage == p.Stage {
			cur = i
		}
	}
	var lines []string
	for i, s := range steps {
		switch {
		case i < cur:
			lines = append(lines, "  "+okStyle.Render("✓")+" "+s.label)
		case i == cur:
			line := "  " + accStyle.Render(spin(now)) + " " + headStyle.Render(s.label)
			if p.Stage == s.stage && p.Total > 0 {
				line += "  " + progressBar(p.Done, p.Total, 24)
			}
			lines = append(lines, line)
		default:
			lines = append(lines, "  "+faintStyle.Render("· "+s.label))
		}
	}
	lines = append(lines, "", dimStyle.Render("  已用时 "+clock(now.Sub(began))+"，看起来停住时也在进行；网络慢时取回和写入会久一些。"))
	return strings.Join(lines, "\n")
}

// progressBar: cells, percentage and amounts.
func progressBar(done, total int64, cells int) string {
	done = min(done, total)
	n := int(float64(done) / float64(total) * float64(cells))
	pct := int(float64(done) / float64(total) * 100)
	return okStyle.Render(strings.Repeat("█", n)) + faintStyle.Render(strings.Repeat("░", cells-n)) +
		fmt.Sprintf(" %3d%%  ", pct) + dimStyle.Render(size(done)+" / "+size(total))
}

func clock(d time.Duration) string {
	s := int(d.Seconds())
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}
