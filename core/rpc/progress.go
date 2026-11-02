package rpc

import (
	"context"
	"encoding/json"
)

// MethodProgress is a notification porter sends while a long call runs
// (a seal, for one); it names the call by its request id. Clients that do
// not know it ignore it: a notification has no id of its own.
const MethodProgress = "progress"

// ProgressParams is one progress report of call ID.
type ProgressParams struct {
	ID    json.RawMessage `json:"id"`
	Stage string          `json:"stage"`
	Done  int64           `json:"done"`
	Total int64           `json:"total"`
}

// ProgressFunc receives a stage's progress; total 0 means unmeasured.
type ProgressFunc func(stage string, done, total int64)

type progressKey struct{}

// WithProgress attaches fn to ctx: porter handlers report through it, and
// on the client the call made with ctx receives the reports.
func WithProgress(ctx context.Context, fn ProgressFunc) context.Context {
	return context.WithValue(ctx, progressKey{}, fn)
}

// ProgressOf is ctx's progress function, nil when none.
func ProgressOf(ctx context.Context) ProgressFunc {
	fn, _ := ctx.Value(progressKey{}).(ProgressFunc)
	return fn
}

// Report sends a stage's progress when ctx carries a progress function.
func Report(ctx context.Context, stage string, done, total int64) {
	if fn := ProgressOf(ctx); fn != nil {
		fn(stage, done, total)
	}
}
