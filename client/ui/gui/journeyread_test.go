package gui

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"valise/core/journey"
	"valise/core/revive"
	"valise/ui/service"
)

func TestJourneyScopeSwitchRejectsPreviousAgentCallbacks(t *testing.T) {
	a, _ := world(t)
	var workers []func()
	a.run = func(f func()) { workers = append(workers, f) }
	var scopes []service.TimelineScanRequest
	a.scanTimeline = func(_ context.Context, req service.TimelineScanRequest, emit func(service.TimelineUpdate)) error {
		scopes = append(scopes, req)
		loc := journey.LocationRef{Kind: "local_repo", ID: "usb", AuthorityID: "local"}
		if !req.LocalOnly {
			loc = journey.LocationRef{Kind: "server_vault", ID: req.ConnectionIDs[0], AuthorityID: req.ConnectionIDs[0]}
		}
		// Deliberately ignore cancellation to emulate an already queued UI reply.
		emit(service.TimelineUpdate{Done: true, Page: service.JourneyPage{Observations: []journey.Observation{{LocationRef: loc, Status: "unknown", Error: "unsupported_capability"}}}})
		return nil
	}
	a.openAgent("home", "xiaoman")
	a.timelinePending = []journey.LocationRef{{Kind: "server_vault", ID: "home"}}
	a.openAgent("cloud", "alan")
	if len(a.timelinePending) != 0 || len(a.timeline.value.Observations) != 0 {
		t.Fatal("new agent inherited previous read state")
	}
	workers[1]()
	workers[0]()
	if !slices.Equal(scopes[0].ConnectionIDs, []string{"cloud"}) || !slices.Equal(scopes[1].ConnectionIDs, []string{"home"}) {
		t.Fatalf("wrong query scopes %+v", scopes)
	}
	if len(a.timeline.value.Observations) != 1 || a.timeline.value.Observations[0].LocationRef.ID != "cloud" {
		t.Fatal("old agent callback overwrote current journey")
	}
	a.refreshLocalTimeline()
	workers[2]()
	if !scopes[2].LocalOnly || len(scopes[2].ConnectionIDs) != 0 {
		t.Fatal("local index refresh queried remote servers")
	}
}

func TestJourneyIncrementalReadKeepsSelectionAndRetriesLocation(t *testing.T) {
	a, tt := world(t)
	a.openAgent("home", "xiaoman")
	a.agentTab = tabJourney
	p := a.timeline.value.Items[0]
	key := pointKey(p)
	a.point = key
	loc := journey.LocationRef{Kind: "server_vault", ID: "home", AuthorityID: "home"}
	page := a.timeline.value
	a.scanTimeline = func(_ context.Context, req service.TimelineScanRequest, emit func(service.TimelineUpdate)) error {
		only := req.Only
		if len(only) != 0 {
			t.Fatalf("unexpected filter: %+v", only)
		}
		emit(service.TimelineUpdate{Page: page, Pending: []journey.LocationRef{loc}})
		tt.Frame()
		tt.Advance(time.Second)
		if !a.timeline.busy || a.point != key || a.agentTab != tabJourney || !tt.HasText(T("journey.scanning", "n", 1)) {
			t.Fatalf("incremental state lost: %q", tt.Texts())
		}
		shot(t, tt, "journey-scanning")
		page.Observations = []journey.Observation{{LocationRef: loc, Status: "unknown", Error: "location_timeout"}}
		emit(service.TimelineUpdate{Page: page, Done: true})
		return nil
	}
	a.refreshTimeline()
	tt.Frame()
	tt.Advance(time.Second)
	click(t, tt, T("journey.failed_locations", "n", 1))
	if !tt.HasText(T("journey.timeout", "location", journeyLocation(loc))) || tt.HasText("发生未预期错误") {
		t.Fatalf("timeout wording: %q", tt.Texts())
	}
	shot(t, tt, "journey-timeout")
	called := false
	a.scanTimeline = func(_ context.Context, req service.TimelineScanRequest, emit func(service.TimelineUpdate)) error {
		only := req.Only
		called = true
		if len(only) != 1 || only[0] != loc {
			t.Fatalf("retried other locations: %+v", only)
		}
		page.Observations = []journey.Observation{{LocationRef: loc, Status: "fresh"}}
		emit(service.TimelineUpdate{Page: page, Done: true})
		return nil
	}
	click(t, tt, T("journey.retry"))
	if !called || a.timeline.busy || a.point != key {
		t.Fatal("retry changed selection or failed to finish")
	}
}

func TestCurrentRestoreHidesDirectoryMappingAndRollbackShowsActualResults(t *testing.T) {
	a, tt, r := restoreWorld(t)
	if tt.HasText(T("restore.path_mapping")) {
		t.Fatal("current restore asks for paths")
	}
	r.roots = "retained-invalid-input"
	r.mode = revive.ModeClone
	tt.Frame()
	if !tt.HasText(T("restore.path_mapping")) {
		t.Fatal("custom placement disappeared from clone")
	}
	r.step = "result"
	r.task = service.RestoreTask{State: "needs_recovery", SwapState: revive.StateRollingBack, Target: "home", Instance: "xiaoman", Rollback: &revive.RollbackEvidence{Directories: "restored", Program: "not_needed", Service: "failed", Health: "not_checked", Problem: "rollback_start_failed"}}
	tt = ui.NewTester(func(c *ui.Context) { a.restoreResult(c, r) }, 1000, 850)
	for _, text := range []string{T("restore.rollback.directory.restored"), T("restore.rollback.service.failed"), T("restore.rollback.health.not_checked"), T("restore.rollback.problem.rollback_start_failed")} {
		if !tt.HasText(text) {
			t.Errorf("missing outcome %q", text)
		}
	}
	shot(t, tt, "rollback-start-failed")
}
