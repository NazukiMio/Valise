package service

import (
	"context"
	"errors"
	"slices"
	"sort"
	"sync"
	"time"

	"valise/core/journey"
)

// TimelineUpdate is a detached full view after one location changes. It is
// independent of fixed-view pagination and makes no recovery claim.
type TimelineUpdate struct {
	Page    JourneyPage
	Pending []journey.LocationRef
	Done    bool
}

type timelineScan struct {
	ctx       context.Context
	reader    *JourneyReader
	emit      func(TimelineUpdate)
	only      []journey.LocationRef
	accept    func(journey.LocationRef) bool
	timeout   time.Duration
	sem       chan struct{}
	wg        sync.WaitGroup
	mu        sync.Mutex
	locations map[journey.LocationRef]journey.Result
	pending   map[journey.LocationRef]bool
	seen      map[journey.LocationRef]bool
	starting  bool
}

func (s *Service) ScanTimeline(ctx context.Context, only []journey.LocationRef, emit func(TimelineUpdate)) error {
	r := s.timelineReader()
	return r.scanTimeline(ctx, only, emit, s.addRemoteTimeline)
}

func (r *JourneyReader) scanTimeline(ctx context.Context, only []journey.LocationRef, emit func(TimelineUpdate), remote func(*timelineScan)) error {
	return r.scanTimelineScoped(ctx, only, emit, remote, nil)
}

func (r *JourneyReader) scanTimelineScoped(ctx context.Context, only []journey.LocationRef, emit func(TimelineUpdate), remote func(*timelineScan), accept func(journey.LocationRef) bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	scan := &timelineScan{ctx: ctx, reader: r, emit: emit, only: only, timeout: 45 * time.Second, sem: make(chan struct{}, 4), locations: map[journey.LocationRef]journey.Result{}, pending: map[journey.LocationRef]bool{}, seen: map[journey.LocationRef]bool{}, starting: true}
	scan.accept = accept
	if r.locationTimeout > 0 {
		scan.timeout = r.locationTimeout
	}
	r.mu.Lock()
	for loc, cached := range r.remoteCache {
		if !scan.includes(loc) {
			continue
		}
		scan.locations[loc] = cloneProtection(cached)
		if len(only) == 0 || slices.Contains(only, loc) {
			scan.locations[loc] = staleTimeline(cached, "refreshing")
		}
	}
	r.mu.Unlock()
	sources, err := r.sources(JourneyListRequest{})
	if err != nil {
		return err
	}
	for _, source := range sources {
		if !scan.wants(source.LocationRef) {
			continue
		}
		scan.add(source.LocationRef, func(ctx context.Context) (journey.Result, error) { return journey.Scan(ctx, []journey.Source{source}) })
	}
	if remote != nil {
		remote(scan)
	}
	scan.mu.Lock()
	scan.starting = false
	scan.publish(false)
	scan.mu.Unlock()
	scan.wg.Wait()
	scan.mu.Lock()
	// A failed server discovery cannot leave its previously seen targets
	// looking as though they are still being read after the scan has ended.
	for loc, cached := range scan.locations {
		if scan.wants(loc) && !scan.seen[loc] {
			scan.locations[loc] = staleTimeline(cached, "location_unavailable")
		}
	}
	scan.publish(true)
	scan.mu.Unlock()
	return ctx.Err()
}

func staleTimeline(cached journey.Result, code string) journey.Result {
	cached = cloneProtection(cached)
	cached.Partial = true
	for i := range cached.Observations {
		cached.Observations[i].Status = "stale"
		cached.Observations[i].Error = code
	}
	for i := range cached.Points {
		for j := range cached.Points[i].Copies {
			cached.Points[i].Copies[j].Observation.Status = "stale"
			cached.Points[i].Copies[j].Observation.Error = code
		}
	}
	return cached
}
func (s *timelineScan) wants(loc journey.LocationRef) bool {
	return s.includes(loc) && (len(s.only) == 0 || slices.Contains(s.only, loc))
}

func (s *timelineScan) includes(loc journey.LocationRef) bool {
	return s.accept == nil || s.accept(loc)
}

// add may also be called by a running server job to schedule the targets it
// has just listed. The parent remains in wg until its children are registered.
func (s *timelineScan) add(loc journey.LocationRef, read func(context.Context) (journey.Result, error)) {
	if !s.wants(loc) {
		return
	}
	s.mu.Lock()
	if s.seen[loc] {
		s.mu.Unlock()
		return
	}
	s.seen[loc] = true
	s.pending[loc] = true
	if old, ok := s.locations[loc]; ok {
		s.locations[loc] = staleTimeline(old, "refreshing")
	}
	if !s.starting {
		s.publish(false)
	}
	s.mu.Unlock()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		var result journey.Result
		var err error
		select {
		case s.sem <- struct{}{}:
			ctx, cancel := context.WithTimeout(s.ctx, s.timeout)
			result, err = read(ctx)
			if err == nil {
				err = ctx.Err()
			}
			cancel()
			<-s.sem
		case <-s.ctx.Done():
			err = s.ctx.Err()
		}
		if err != nil {
			code := "location_unavailable"
			if errors.Is(err, context.DeadlineExceeded) {
				code = "location_timeout"
			}
			result = journeyUnavailable(loc, code)
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.pending, loc)
		if s.ctx.Err() != nil {
			return
		}
		s.reader.mu.Lock()
		result = s.reader.retainRemote(result)
		s.reader.mu.Unlock()
		s.locations[loc] = result
		if !s.starting {
			s.publish(false)
		}
	}()
}

// Called with the scan mutex held. Callbacks are serialized; consumers must
// only enqueue updates, not start or wait for another scan from the callback.
func (s *timelineScan) publish(done bool) {
	if s.emit == nil || s.ctx.Err() != nil {
		return
	}
	var parts []journey.Result
	for _, r := range s.locations {
		parts = append(parts, r)
	}
	result := journey.Merge(parts...)
	sort.Slice(result.Observations, func(i, j int) bool {
		a, b := result.Observations[i].LocationRef, result.Observations[j].LocationRef
		return a.Kind+a.AuthorityID+a.ID < b.Kind+b.AuthorityID+b.ID
	})
	var pending []journey.LocationRef
	for loc := range s.pending {
		pending = append(pending, loc)
	}
	sort.Slice(pending, func(i, j int) bool {
		a, b := pending[i], pending[j]
		return a.Kind+a.AuthorityID+a.ID < b.Kind+b.AuthorityID+b.ID
	})
	s.emit(cloneProtection(TimelineUpdate{Page: JourneyPage{Items: result.Points, Observations: result.Observations, Gaps: result.Gaps, Partial: result.Partial}, Pending: pending, Done: done}))
}
