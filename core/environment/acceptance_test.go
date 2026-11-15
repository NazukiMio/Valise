package environment

import (
	"reflect"
	"strings"
	"testing"
)

func TestDirectPolicyOnlyEnablesSeparateAcceptedOldCleanup(t *testing.T) {
	id := strings.Repeat("a", 32)
	r := trial(t, initial(t, Parallel), id)
	if _, err := r.SetMode(Direct); err == nil {
		t.Fatal("unaccepted candidate disabled protection")
	}
	r, err := r.ConfirmUsable(id, r.Operation.Candidate)
	r = must(t, r, err)
	if _, err := r.BeginCleanup(id, r.Operation.Original); err == nil {
		t.Fatal("retaining mode offered old cleanup")
	}
	before := r
	r, err = r.SetMode(Direct)
	r = must(t, r, err)
	if r.Active != before.Active || !reflect.DeepEqual(r.Slots, before.Slots) || !reflect.DeepEqual(r.Operation, before.Operation) {
		t.Fatal("saving direct policy changed environments")
	}
	if _, err := r.BeginCleanup(id, r.Operation.Candidate); err != ErrStale {
		t.Fatal("cleanup accepted current active revision", err)
	}
	r, err = r.BeginCleanup(id, r.Operation.Original)
	r = must(t, r, err)
	if _, err := r.SetMode(Parallel); err == nil {
		t.Fatal("mode changed during cleanup")
	}
	r, err = r.CleanupCompleted(id)
	r = must(t, r, err)
	if r.Mode != Direct || r.Operation.Mode != Parallel || !r.Operation.Confirmed || !r.Operation.OriginalCleaned || r.Active != B {
		t.Fatal("cleanup rewrote frozen policy or active acceptance")
	}
}

func TestCancelledCandidateCleanupKeepsCurrentSourceAndNextRevision(t *testing.T) {
	id := strings.Repeat("c", 32)
	r := initial(t, Compact)
	r, err := r.Begin(id, snapshot(1))
	r = must(t, r, err)
	r, err = r.CancelPreparation(id)
	r = must(t, r, err)
	original, target := r.Operation.Original, r.Operation.Candidate
	if _, err := r.PreparationCleaned(id, original); err != ErrStale {
		t.Fatal("cleanup targeted current original", err)
	}
	r, err = r.PreparationCleaned(id, target)
	r = must(t, r, err)
	if r.Active != original.Slot || r.Slots[0].State != Active || r.Slots[1].State != Empty || !r.Operation.CandidateCleaned {
		t.Fatal("cancel cleanup changed original")
	}
	r, err = r.Begin(strings.Repeat("d", 32), snapshot(2))
	r = must(t, r, err)
	if r.Operation.Original != original || r.Operation.Candidate.Slot != target.Slot || r.Operation.Candidate.Revision != target.Revision+1 {
		t.Fatal("cancel cleanup reset revision or source")
	}
}

func TestAcceptedEnvironmentCanOnlyExplicitlyReturnWhileOriginalRetained(t *testing.T) {
	for _, mode := range []Mode{Parallel, Compact} {
		t.Run(string(mode), func(t *testing.T) {
			id := strings.Repeat("a", 32)
			r := trial(t, initial(t, mode), id)
			r, err := r.ConfirmUsable(id, r.Operation.Candidate)
			r = must(t, r, err)
			if next, err := r.BeginRollback(id, "gateway_failed"); err == nil || !reflect.DeepEqual(next, r) {
				t.Fatal("accepted environment automatically rolled back")
			}
			back, err := r.BeginRollback(id, "user_revert")
			back = must(t, back, err)
			if back.Operation.Confirmed || back.Operation.Phase != RollingBack {
				t.Fatal("return kept candidate acceptance active")
			}
			if _, err := back.ResumeOriginal(id); err == nil {
				t.Fatal("accepted return skipped candidate stop")
			}
			back, err = back.CandidateStopped(id)
			back = must(t, back, err)
			back, err = back.ResumeOriginal(id)
			back = must(t, back, err)
			back, err = back.OriginalHealthy(id)
			back = must(t, back, err)
			if back.Active != A || back.Operation.Confirmed {
				t.Fatal("return lost original environment")
			}
			// A later operation owns the slots; the old human decision is stale.
			reused, err := r.Begin(strings.Repeat("b", 32), snapshot(300))
			reused = must(t, reused, err)
			if _, err := reused.BeginRollback(id, "user_revert"); err != ErrStale {
				t.Fatal("historical acceptance controlled reused slot", err)
			}
			if mode == Compact {
				cleaning, err := r.BeginCleanup(id, r.Operation.Original)
				cleaning = must(t, cleaning, err)
				cleaned, err := cleaning.CleanupCompleted(id)
				cleaned = must(t, cleaned, err)
				if _, err := cleaned.BeginRollback(id, "user_revert"); err == nil {
					t.Fatal("cleaned original could be returned to")
				}
			}
		})
	}
}
