package gui

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/ui/service"
)

// restoreProgressTracker tracks both overall batch member progress
// (StageBundleMember) and the current member's stage + byte progress.
type restoreProgressTracker struct {
	stage       string
	done        int64
	total       int64
	memberDone  int64
	memberTotal int64
}

func (p *restoreProgressTracker) apply(ev service.Progress) {
	if ev.Stage == service.StageBundleMember {
		p.memberDone = ev.Done
		p.memberTotal = ev.Total
		p.stage = ""
		p.done = 0
		p.total = 0
		return
	}
	p.stage = ev.Stage
	p.done = ev.Done
	p.total = ev.Total
}

// withProgressTracker attaches a throttled service.WithProgress callback to ctx
// that updates dst on the UI loop without dropping stage or member transitions.
func (a *App) withProgressTracker(ctx context.Context, dst *restoreProgressTracker) context.Context {
	var mu sync.Mutex
	lastStage := ""
	lastAt := time.Time{}
	return service.WithProgress(ctx, func(ev service.Progress) {
		mu.Lock()
		defer mu.Unlock()
		now := time.Now()
		if ev.Stage == service.StageBundleMember || ev.Stage != lastStage || now.Sub(lastAt) >= 100*time.Millisecond || (ev.Total > 0 && ev.Done >= ev.Total) {
			lastStage, lastAt = ev.Stage, now
			a.update(func() { dst.apply(ev) })
		}
	})
}

func restoreAnimStageIndex(stage, step string) int {
	switch stage {
	case service.StageStage:
		return 1
	case service.StageVerify:
		return 2
	case service.StageSwap:
		return 3
	case "finalize", "post_migrate", "mcp":
		return 4
	}
	if step == "swap" {
		return 3
	}
	return 0
}

func restoreAnimStageLabels() [5]string {
	return [5]string{
		T("restore.anim.stage_pull"),
		T("restore.anim.stage_stage"),
		T("restore.anim.stage_verify"),
		T("restore.anim.stage_swap"),
		T("restore.anim.stage_finalize"),
	}
}

func renderRestoreAnimProgress(c *ui.Context, p restoreProgressTracker, startedAt time.Time, step, fallbackTitle string) {
	c.After(150 * time.Millisecond)
	now := c.Now()
	if startedAt.IsZero() {
		startedAt = now
	}
	frame := int(now.UnixMilli()/150) % 4
	active := restoreAnimStageIndex(p.stage, step)
	labels := restoreAnimStageLabels()

	title := restoreStageText(p.stage)
	if p.stage == "" && fallbackTitle != "" {
		title = fallbackTitle
	}
	ui.Row(c).Gap(spInside).AlignItems(ui.Center).Wrap().Children(func() {
		ui.Spinner(c)
		ui.Text(c, title).Bold()
		if p.memberTotal > 0 {
			curIdx := p.memberDone
			if curIdx <= 0 {
				curIdx = 1
			}
			plainTag(c, T("restore.anim.member_progress", "done", curIdx, "total", p.memberTotal))
		}
	})

	subPanel(ui.Column(c)).Gap(spInside).Children(func() {
		ui.Row(c).Gap(spHair + 2).AlignItems(ui.Center).Role(ui.RoleGroup).Label(labels[active]).Children(func() {
			for i, name := range labels {
				ic := c.Key("ra-st-" + strconv.Itoa(i))
				if i > 0 {
					barColor := palette.Line
					if i <= active {
						barColor = palette.Brand.Alpha(0.75)
					}
					ui.Box(c.Key("ra-bar-" + strconv.Itoa(i))).Height(2).Grow(1).MinWidth(10).Background(barColor)
				}
				ui.Column(ic).Gap(4).AlignItems(ui.Center).Shrink(0).Children(func() {
					badge := ui.Box(ic).Size(22, 22).Radius(11).Justify(ui.Center).AlignItems(ui.Center).
						Transition(ui.ElementTransition{Duration: motionBase, Colors: true})
					ink := palette.TextSub
					switch {
					case i < active:
						badge.Background(palette.Ok.Alpha(0.16)).Children(func() {
							ui.Icon(ic, icons.check).FontSize(13).TextColor(palette.Ok)
						})
					case i == active:
						ink = palette.Text
						badge.Background(palette.Brand).Children(func() {
							renderAnimPulseDots(ic, frame)
						})
					default:
						badge.Border(1.5, palette.Line).Children(func() {
							muted(ui.Text(ic, strconv.Itoa(i+1))).FontSize(fsCaption)
						})
					}
					lbl := ui.Text(ic, name).FontSize(fsCaption).TextColor(ink)
					if i == active {
						lbl.Bold()
					}
				})
			}
		})
	})

	muted(ui.Text(c, T("restore.elapsed", "duration", backupDuration(now.Sub(startedAt))))).Wrap()
	if p.total > 0 {
		ratio := float64(p.done) / float64(p.total)
		if ratio < 0 {
			ratio = 0
		} else if ratio > 1 {
			ratio = 1
		}
		ui.Progress(c, ratio)
		muted(ui.Text(c, T("restore.bytes", "done", size(p.done), "total", size(p.total)))).Wrap()
	}
}

func renderAnimPulseDots(c *ui.Context, frame int) {
	ui.Row(c).Gap(2).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
		for d := 0; d < 3; d++ {
			alpha := float32(0.35)
			if frame == d || (frame == 3 && d == 1) {
				alpha = 1.0
			}
			ui.Box(c.Key("dot-"+strconv.Itoa(d))).Size(3.5, 3.5).Radius(2).Background(palette.Card.Alpha(alpha))
		}
	})
}
