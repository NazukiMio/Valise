package gui

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/valise"
)

// job is one action running on a server from its settings: what it is
// doing now, since when, and how to stop it (docs/UI.md 7.5 “等待时说清在
// 做什么”).
type job struct {
	busy    bool
	err     error
	doing   string
	doingAt time.Time
	cancel  context.CancelFunc
}

// start runs fn off the main thread; done gets its error on the main
// thread. A stopped job reports that it was stopped.
func (a *App) start(j *job, timeout time.Duration, fn func(ctx context.Context) error, done func(error)) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	j.busy, j.err, j.cancel, j.doing, j.doingAt = true, nil, cancel, T("prepare.started"), time.Now()
	a.run(func() {
		defer cancel()
		if a.svc != nil {
			stopOp := a.svc.BeginOperation()
			defer stopOp()
		}
		err := fn(ctx)
		if ctx.Err() == context.Canceled {
			err = valise.New("cancelled", T("prepare.stopped"), T("prepare.stopped_hint"))
		}
		a.update(func() {
			j.busy, j.err, j.cancel, j.doing = false, err, nil, ""
			if done != nil {
				done(err)
			}
		})
	})
}

// says records what the job is doing now, from any goroutine.
func (a *App) says(j *job) func(string) {
	return func(what string) { a.update(func() { j.doing, j.doingAt = what, time.Now() }) }
}

func (a *App) stopJob(j *job) {
	if j.cancel != nil {
		j.cancel()
	}
	a.dropServerPrompts()
}

// jobLine shows a running job: the sub-step, how long it has waited, and
// a stop button.
func (a *App) jobLine(c *ui.Context, j *job) {
	c.After(time.Second)
	ui.Row(c).Gap(10).Padding(10, 12).Radius(10).Background(palette.Board).AlignItems(ui.Center).Children(func() {
		ui.Spinner(c)
		text := a.doingText(j.doing, j.doingAt, c.Now())
		muted(ui.Text(c, text)).Wrap().Grow(1)
		ui.Button(c, T("common.stop")).OnClick(func() { a.stopJob(j) })
	})
}

// doingText is a sub-step with how long it has waited, or that OpenSSH is
// waiting for the user's answer in its dialog.
func (a *App) doingText(doing string, at, now time.Time) string {
	for _, p := range a.asks {
		if p.kind == askServer {
			return T("prepare.ssh_asking")
		}
	}
	text := orText(doing, T("prepare.running"))
	if secs := int(now.Sub(at).Seconds()); !at.IsZero() && secs >= 3 {
		return T("prepare.waited", "what", text, "n", secs)
	}
	return T("prepare.doing", "what", text)
}

// needTag is the small tag that says what a button will ask for.
func needTag(c *ui.Context, s string) {
	ui.Box(c.Key("need-"+s)).Padding(1, 8).Radius(999).Background(palette.Board).Children(func() {
		ui.Text(c, s).FontSize(fsCaption).TextColor(palette.TextSub)
	})
}
