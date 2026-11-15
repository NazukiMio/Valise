package gui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
	"valise/core/journey"
	"valise/core/valise"
	"valise/ui/service"
)

func (a *App) refreshTimeline() {
	if a.timeline.busy {
		return
	}
	a.readTimeline(nil)
}

func (a *App) refreshLocalTimeline() {
	a.timelineScopeID = ""
	a.timelineScope = service.TimelineScanRequest{LocalOnly: true}
	a.readTimeline(nil)
}

func (a *App) ensureTimelineScope(server, instance string) {
	connections := service.JourneyConnections(a.agents.value.Items, server, instance)
	key := server + "/" + instance + "/" + strings.Join(connections, "/")
	if key == a.timelineScopeID {
		return
	}
	a.timelineScopeID = key
	a.timelineScope = service.TimelineScanRequest{ConnectionIDs: connections}
	// Cached points can still populate summaries. A previous agent's location
	// errors and pending counts cannot populate this agent's status row.
	a.timeline.value.Observations, a.timeline.value.Gaps = nil, nil
	a.timelinePending = nil
	a.readTimeline(nil)
}

// Each completed location updates the existing page. A generation check drops
// callbacks queued by a superseded read, without resetting selection or folds.
func (a *App) readTimeline(only []journey.LocationRef) {
	if a.timelineCancel != nil {
		a.timelineCancel()
	}
	a.timelineScanID++
	id := a.timelineScanID
	ctx, cancel := context.WithCancel(context.Background())
	a.timelineCancel = cancel
	a.timeline.busy, a.timeline.started, a.timeline.err = true, true, nil
	req := a.timelineScope
	req.ConnectionIDs = slices.Clone(req.ConnectionIDs)
	req.Only = slices.Clone(only)
	a.run(func() {
		defer cancel()
		err := a.scanTimeline(ctx, req, func(update service.TimelineUpdate) {
			a.update(func() {
				if id != a.timelineScanID {
					return
				}
				a.timeline.value = update.Page
				a.timeline.busy = !update.Done
				a.timelinePending = update.Pending
				a.timeline.at = time.Now()
			})
		})
		a.update(func() {
			if id != a.timelineScanID {
				return
			}
			a.timeline.busy = false
			a.timelineCancel = nil
			a.timelinePending = nil
			if err != nil && !errors.Is(err, context.Canceled) {
				if errors.Is(err, context.DeadlineExceeded) {
					err = valise.New("timeline_timeout", T("journey.read_timeout"), T("journey.read_retry"))
				}
				a.timeline.err = err
			}
		})
	})
}
