package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"valise/client/config"
	"valise/core/journey"
	"valise/core/vault"
)

func TestJourneyStreamShowsLocalBeforeFakeSSHTimeoutAndRetriesOneLocation(t *testing.T) {
	s, _, _ := acceptedPlanWorld(t)
	if _, err := s.AddServers("box"); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	now := time.Now().UTC()
	journeyFixture(t, root, vault.NewID("v", now), vault.NewID("s", now), "soul", now)
	s.Config.Repos = []config.Repo{{Name: "disk", Path: root}}
	s.timelineReader().locationTimeout = 500 * time.Millisecond
	t.Setenv("FAKESSH_HANG", "box")
	var updates []TimelineUpdate
	err := s.ScanTimeline(context.Background(), nil, func(u TimelineUpdate) { updates = append(updates, u) })
	if err != nil {
		t.Fatal(err)
	}
	shownEarly := false
	for _, u := range updates {
		if len(u.Page.Items) == 1 && len(u.Pending) > 0 && !u.Done {
			shownEarly = true
		}
	}
	last := updates[len(updates)-1]
	if !shownEarly || !last.Done || len(last.Page.Items) != 1 || len(last.Pending) != 0 {
		t.Fatalf("early=%v final=%+v", shownEarly, last)
	}
	loc := journey.LocationRef{Kind: "server_vault", ID: "box", AuthorityID: "box"}
	found := false
	for _, obs := range last.Page.Observations {
		if obs.LocationRef == loc && obs.Error == "location_timeout" {
			found = true
		}
	}
	if !found {
		t.Fatalf("timeout missing: %+v", last.Page.Observations)
	}
	// The healthy retry replaces just this location and retains the local point.
	t.Setenv("FAKESSH_HANG", "")
	s.timelineReader().locationTimeout = 10 * time.Second
	err = s.ScanTimeline(context.Background(), []journey.LocationRef{loc}, func(u TimelineUpdate) { last = u })
	if err != nil || !last.Done || len(last.Page.Items) != 1 {
		t.Fatalf("retry %+v %v", last, err)
	}
	for _, obs := range last.Page.Observations {
		if obs.Status != "fresh" {
			t.Fatalf("retry did not replace failed observation: %+v", obs)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	called := false
	err = s.ScanTimeline(ctx, nil, func(u TimelineUpdate) { called = true; cancel() })
	if !called || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled=%v err=%v", called, err)
	}
}

func TestJourneyStreamReplacesCopiesInsteadOfAppending(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	journeyFixture(t, root, vault.NewID("v", now), vault.NewID("s", now), "soul", now)
	r := NewJourneyReader([]config.Repo{{Name: "disk", Path: root}})
	var last TimelineUpdate
	for range 3 {
		if err := r.scanTimeline(context.Background(), nil, func(u TimelineUpdate) { last = u }, nil); err != nil {
			t.Fatal(err)
		}
		if len(last.Page.Items) != 1 || len(last.Page.Items[0].Copies) != 1 {
			t.Fatalf("duplicate after refresh: %+v", last)
		}
	}
	// Cached targets whose server cannot list them must not remain 'refreshing'.
	loc := journey.LocationRef{Kind: "legacy_target", ID: "disk", AuthorityID: "box"}
	r.remoteCache[loc] = journey.Result{Observations: []journey.Observation{{LocationRef: loc, Status: "fresh"}}}
	if err := r.scanTimeline(context.Background(), nil, func(u TimelineUpdate) { last = u }, nil); err != nil {
		t.Fatal(err)
	}
	for _, obs := range last.Page.Observations {
		if obs.LocationRef == loc && obs.Error != "location_unavailable" {
			t.Fatalf("unvisited cache still refreshing: %+v", obs)
		}
	}
}
