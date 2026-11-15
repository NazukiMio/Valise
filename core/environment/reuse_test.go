package environment

import (
	"reflect"
	"strings"
	"testing"
)

func TestReuseOnlyRecordsConsumedHistoricalInactiveRevision(t *testing.T) {
	first, next := strings.Repeat("a", 32), strings.Repeat("b", 32)
	r := trial(t, initial(t, Parallel), first)
	r, err := r.ConfirmUsable(first, r.Operation.Candidate)
	r = must(t, r, err)
	old := r.Operation.Original
	r, err = r.Begin(next, snapshot(300))
	r = must(t, r, err)
	for _, bad := range []struct {
		id, owner string
		target    Ref
	}{
		{first, first, old},
		{next, next, old},
		{next, first, r.Operation.Original},
		{next, first, r.Operation.Candidate},
		{next, first, Ref{old.Slot, 0}},
		{next, first, Ref{old.Slot, 99}},
	} {
		got, err := r.ReuseCompleted(bad.id, bad.owner, bad.target)
		if err == nil || !reflect.DeepEqual(got, r) {
			t.Fatal("invalid reuse changed registry", bad, err)
		}
	}
	got, err := r.ReuseCompleted(next, first, old)
	got = must(t, got, err)
	if !got.History[0].OriginalCleaned || got.Active != r.Active || got.Slots != r.Slots || *got.Operation != *r.Operation {
		t.Fatal("reuse changed current environment or human acceptance", got)
	}
}

func TestReuseCancelledCandidateKeepsCurrentSourceAndPolicy(t *testing.T) {
	first, cancelled, next := strings.Repeat("a", 32), strings.Repeat("b", 32), strings.Repeat("c", 32)
	r := trial(t, initial(t, Parallel), first)
	r, err := r.ConfirmUsable(first, r.Operation.Candidate)
	r = must(t, r, err)
	r, err = r.Begin(cancelled, snapshot(300))
	r = must(t, r, err)
	target := r.Operation.Candidate
	r, err = r.CancelPreparation(cancelled)
	r = must(t, r, err)
	r, err = r.Begin(next, snapshot(400))
	r = must(t, r, err)
	got, err := r.ReuseCompleted(next, cancelled, target)
	got = must(t, got, err)
	if !got.History[1].CandidateCleaned || got.History[0] != r.History[0] || got.Active != r.Active || got.Slots != r.Slots || *got.Operation != *r.Operation || got.Mode != Parallel {
		t.Fatal("cancelled candidate reuse changed active source or historical acceptance")
	}
}
