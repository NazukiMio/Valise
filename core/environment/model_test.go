package environment

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"valise/core/identity"
	"valise/core/vault"
)

var agent = identity.AgentRef{ServerUID: "server", InstallID: "install", InstanceID: "agent", Generation: "generation"}

func snapshot(n int) identity.SnapshotRef {
	return identity.SnapshotRef{VaultID: vault.NewID("v", time.Unix(100, 0)), SnapshotID: vault.NewID("s", time.Unix(int64(n), 0))}
}

func must(t *testing.T, r Registry, err error) Registry {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	return r
}

func initial(t *testing.T, mode Mode) Registry {
	r, err := New(agent)
	r = must(t, r, err)
	r, err = r.SetMode(mode)
	return must(t, r, err)
}

func prepare(t *testing.T, r Registry, id string) Registry {
	r, err := r.Begin(id, snapshot(200))
	r = must(t, r, err)
	if r.Active != r.Operation.Original.Slot || r.slot(r.Active).State != Active {
		t.Fatal("building changed active environment")
	}
	r, err = r.Prepared(id)
	return must(t, r, err)
}

func trial(t *testing.T, r Registry, id string) Registry {
	r = prepare(t, r, id)
	r, err := r.StopOriginal(id)
	r = must(t, r, err)
	r, err = r.OriginalStopped(id)
	r = must(t, r, err)
	r, err = r.StartCandidate(id)
	r = must(t, r, err)
	r, err = r.GatewayPassed(id)
	return must(t, r, err)
}

func TestAlternatingRestoreAndStaleCommands(t *testing.T) {
	r := initial(t, Parallel)
	for i := 1; i <= 5; i++ {
		id := strings.Repeat(string(rune('a'+i)), 32)
		old := r.Active
		r = trial(t, r, id)
		if r.Active == old || r.slot(old).State != Stopped {
			t.Fatal("restore did not alternate or retain original")
		}
		before, _ := json.Marshal(r)
		bad, err := r.Begin(strings.Repeat("f", 32), snapshot(300))
		if !errors.Is(err, ErrState) || !reflect.DeepEqual(bad, r) {
			t.Fatal("unconfirmed trial allowed a new restore or mutated input")
		}
		if _, err := r.ConfirmUsable(id, Ref{r.Active, 999}); err != ErrStale {
			t.Fatal("accepted stale confirmation")
		}
		after, _ := json.Marshal(r)
		if string(before) != string(after) {
			t.Fatal("failed transition changed shared operation")
		}
		var e error
		r, e = r.ConfirmUsable(id, r.Operation.Candidate)
		r = must(t, r, e)
		if _, err := r.BeginCleanup(id, r.Operation.Original); err != ErrState {
			t.Fatal("full parallel offered compact cleanup")
		}
	}
	if len(r.History) != 4 || r.Active != B {
		t.Fatal(r)
	}
	if _, err := r.Begin(strings.Repeat("b", 32), snapshot(400)); err == nil {
		t.Fatal("replayed historical operation")
	}
}

func TestCompactRequiresHumanAndSeparateCleanup(t *testing.T) {
	id := strings.Repeat("a", 32)
	r := trial(t, initial(t, Compact), id)
	old := r.Operation.Original
	if _, err := r.BeginCleanup(id, old); err == nil {
		t.Fatal("gateway pass authorized cleanup")
	}
	r, err := r.ConfirmUsable(id, r.Operation.Candidate)
	r = must(t, r, err)
	if r.slot(old.Slot).State != Stopped || r.Operation.OriginalCleaned {
		t.Fatal("confirmation cleaned original")
	}
	r, err = r.BeginCleanup(id, old)
	r = must(t, r, err)
	if _, err := r.Begin(strings.Repeat("b", 32), snapshot(400)); err == nil {
		t.Fatal("started restore while cleanup pending")
	}
	r, err = r.CleanupFailed(id)
	r = must(t, r, err)
	if !r.Operation.Confirmed || r.Active != B {
		t.Fatal("cleanup failure rewrote accepted runtime")
	}
	r, err = r.BeginCleanup(id, old)
	r = must(t, r, err)
	r, err = r.CleanupCompleted(id)
	r = must(t, r, err)
	if r.slot(old.Slot).State != Empty || r.Active != B {
		t.Fatal(r)
	}
	r = trial(t, r, strings.Repeat("b", 32))
	if _, err := r.BeginCleanup(id, old); err == nil {
		t.Fatal("old cleanup command accepted after slot reuse")
	}
}

func TestRollbackRequiresStoppedCandidateAndRecordsFailure(t *testing.T) {
	id := strings.Repeat("a", 32)
	for _, phase := range []Phase{Building, Prepared, Stopping, OriginalDown, Starting, AwaitingHuman} {
		t.Run(string(phase), func(t *testing.T) {
			r := initial(t, Parallel)
			r, err := r.Begin(id, snapshot(200))
			r = must(t, r, err)
			for r.Operation.Phase != phase {
				var err error
				switch r.Operation.Phase {
				case Building:
					r, err = r.Prepared(id)
				case Prepared:
					r, err = r.StopOriginal(id)
				case Stopping:
					r, err = r.OriginalStopped(id)
				case OriginalDown:
					r, err = r.StartCandidate(id)
				case Starting:
					r, err = r.GatewayPassed(id)
				}
				r = must(t, r, err)
			}
			r, err = r.BeginRollback(id, "gateway_failed")
			r = must(t, r, err)
			if _, err := r.ResumeOriginal(id); err == nil {
				t.Fatal("original resumed before candidate verified stopped")
			}
			r, err = r.CandidateStopped(id)
			r = must(t, r, err)
			r, err = r.ResumeOriginal(id)
			r = must(t, r, err)
			// Serialization/restart is observation only, never replay.
			b, _ := json.Marshal(r)
			var loaded Registry
			if json.Unmarshal(b, &loaded) != nil || loaded.Validate() != nil || loaded.Active != "" {
				t.Fatal("pending resume misrepresented as healthy")
			}
			r, err = loaded.OriginalHealthy(id)
			r = must(t, r, err)
			if r.Active != A || r.Operation.Phase != RolledBack || r.Operation.FailureCode != "gateway_failed" {
				t.Fatal("rollback lost original failure")
			}
		})
	}
}

func TestNoModeFallbackAndGenerationSeparation(t *testing.T) {
	r := initial(t, Direct)
	if _, err := r.Begin(strings.Repeat("a", 32), snapshot(200)); err == nil {
		t.Fatal("direct mode entered parallel executor")
	}
	r = prepare(t, initial(t, Compact), strings.Repeat("a", 32))
	if _, err := r.SetMode(Direct); err == nil || r.Operation.Mode != Compact {
		t.Fatal("operation policy changed in flight")
	}
	other := agent
	other.Generation = "other"
	if Key(agent) == Key(other) {
		t.Fatal("different generations share registry")
	}
}

func TestPreparationCancellationRetainsOriginalWithoutHealthOrAcceptance(t *testing.T) {
	id := strings.Repeat("a", 32)
	r := prepare(t, initial(t, Compact), id)
	cancelled, err := r.CancelPreparation(id)
	cancelled = must(t, cancelled, err)
	if cancelled.Active != A || cancelled.Slots[0].Ref != r.Slots[0].Ref || cancelled.Operation.Phase != Cancelled || cancelled.Operation.Confirmed || cancelled.Operation.OriginalCleaned || !OperationFinished(cancelled.Operation.Phase) {
		t.Fatal(cancelled)
	}
	if _, err := cancelled.ConfirmUsable(id, cancelled.Operation.Candidate); err == nil {
		t.Fatal("cancelled candidate accepted")
	}
	next, err := cancelled.Begin(strings.Repeat("b", 32), snapshot(300))
	next = must(t, next, err)
	if next.Operation.Candidate.Slot != B || next.Operation.Candidate.Revision != r.Operation.Candidate.Revision+1 || len(next.History) != 1 || next.History[0].Phase != Cancelled {
		t.Fatal("cancelled candidate not independently reusable", next)
	}
	stopping, err := r.StopOriginal(id)
	stopping = must(t, stopping, err)
	if got, err := stopping.CancelPreparation(id); err == nil || !reflect.DeepEqual(got, stopping) {
		t.Fatal("stop intent cancelled as preparation", got, err)
	}
	live := trial(t, initial(t, Parallel), id)
	if got, err := live.CancelPreparation(id); err == nil || !reflect.DeepEqual(got, live) {
		t.Fatal("active candidate cancelled without return", got, err)
	}
}
