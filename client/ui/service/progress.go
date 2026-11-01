package service

import (
	"context"
	"io"
)

// Progress is one step of a long operation, for frontends that show a
// bar. Total is 0 when the step cannot be measured.
type Progress struct {
	Stage string
	Done  int64
	Total int64
}

// Stages reported by PlanRevive, ExecuteRevive and Pull.
const (
	StagePull    = "pull"    // copying the vault leaf to this computer
	StageOpen    = "open"    // decrypting the snapshot here
	StageInspect = "inspect" // asking the target for its facts
	StageStage   = "stage"   // writing the files into the stage directory
	StageVerify  = "verify"  // checking the stage against the manifest
	StageSwap    = "swap"    // stop, swap, start, health check
)

type progressKey struct{}

// WithProgress returns a context whose long operations report to fn. fn is
// called from the operation's goroutine and must not block.
func WithProgress(ctx context.Context, fn func(Progress)) context.Context {
	return context.WithValue(ctx, progressKey{}, fn)
}

func report(ctx context.Context, stage string, done, total int64) {
	if fn, ok := ctx.Value(progressKey{}).(func(Progress)); ok {
		fn(Progress{Stage: stage, Done: done, Total: total})
	}
}

// countingWriter reports bytes written for one stage.
type countingWriter struct {
	ctx   context.Context
	stage string
	done  *int64
	total int64
	w     io.Writer
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	*c.done += int64(n)
	report(c.ctx, c.stage, *c.done, c.total)
	return n, err
}
