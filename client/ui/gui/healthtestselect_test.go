package gui

import (
	"context"
	"testing"
	"time"

	"valise/client/config"
	"valise/core/journey"
	"valise/ui/service"
)

func TestHealthTestSelectionFreezesOlderPointAndCopy(t *testing.T) {
	a, tt := healthRestoreWorld(t)
	a.svc.Config.Repos = []config.Repo{{Name: "disk", Path: t.TempDir()}}
	p := a.timeline.value.Items[1]
	copy := p.Copies[0]
	copy.LocationRef = journey.LocationRef{Kind: "local_repo", ID: "disk", AuthorityID: "local"}
	p.Copies = append(p.Copies, copy)
	a.timeline.value.Items[1] = p
	click(t, tt, T("restoretest.open"))
	label := T("restoretest.point_label", "time", p.CreatedAt.Local().Format("2006-01-02 15:04:05"), "scope", tierText(p.Scope.Tier))
	click(t, tt, label)
	click(t, tt, journeyLocation(copy.LocationRef))
	flow := a.restoreTest
	if flow.selected == nil || flow.selected.SnapshotRef != p.SnapshotRef || flow.sources[flow.source].Copy.LocationRef != copy.LocationRef {
		t.Fatal("point/copy selection was lost")
	}
	newer := p
	newer.SnapshotRef.SnapshotID = "newly-discovered"
	newer.CreatedAt = p.CreatedAt.Add(48 * time.Hour)
	a.timeline.value.Items = append([]journey.TimelinePoint{newer}, a.timeline.value.Items...)
	tt.Frame()
	if flow.selected.SnapshotRef != p.SnapshotRef || flow.sources[flow.source].Copy.LocationRef != copy.LocationRef {
		t.Fatal("incremental scan replaced human choice")
	}
	shot(t, tt, "restore-test-selected-point-copy")
	var workers []func()
	a.run = func(f func()) { workers = append(workers, f) }
	called := false
	a.runRestoreTest = func(_ context.Context, req service.SelectedRestoreTest) (*service.RestoreTest, error) {
		called = true
		if req.Snapshot != p.SnapshotRef || req.Source.Copy.LocationRef != copy.LocationRef || req.Source.Copy.HeaderDigest != copy.HeaderDigest {
			t.Fatal("execution changed point/copy after confirmation")
		}
		return &service.RestoreTest{State: "passed", OK: true, Snapshot: req.Snapshot.SnapshotID}, nil
	}
	click(t, tt, T("restoretest.start"))
	if called || a.keyFlow == nil || a.keyFlow.then == nil {
		t.Fatal("key gate was bypassed")
	}
	// A late metadata refresh cannot change what the person already confirmed
	// while the separate key gate completes.
	flow.selected = &newer
	resume := a.keyFlow.then
	a.keyFlow = nil
	resume()
	workers[0]()
	if !called {
		t.Fatal("confirmed exact selection did not execute")
	}
}

func TestJourneyOpensTestForThatExactPoint(t *testing.T) {
	a, tt := healthRestoreWorld(t)
	p := a.timeline.value.Items[2]
	a.agentTab, a.point = tabJourney, pointKey(p)
	tt.Frame()
	tt.Advance(time.Second)
	click(t, tt, T("restoretest.point_open"))
	if a.restoreTest == nil || a.restoreTest.selected == nil || a.restoreTest.selected.SnapshotRef != p.SnapshotRef || !a.restoreTest.confirm || a.agentTab != tabHealth {
		t.Fatal("journey test silently changed the selected point")
	}
	hasAll(t, tt.Texts(), T("restoretest.choose_title"), p.CreatedAt.Local().Format("2006-01-02 15:04:05"))
	shot(t, tt, "journey-selected-point-test")
}
