package revive

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"valise/core/revive"
)

func TestManagedDataRevertsPartialRootRenamesBeforePhaseSaved(t *testing.T) {
	for _, candidatePlaced := range []bool{false, true} {
		t.Run(map[bool]string{false: "first-original-moved", true: "first-candidate-placed"}[candidatePlaced], func(t *testing.T) {
			w := newManagedWorld(t, true)
			w.begin(t)
			w.originalDown(t)
			j, _ := w.r.Store.Load(w.p.OpID)
			// Simulate a process interruption between roots, before the next
			// phase is saved. No fake systemd evidence is created by this test.
			state := revive.StateStopped
			if candidatePlaced {
				state = revive.StateLiveMoved
			}
			if err := w.r.Store.Set(j, state); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(j.Roots[0].Live, j.Roots[0].Prev); err != nil {
				t.Fatal(err)
			}
			if candidatePlaced {
				if err := os.Rename(j.Roots[0].Stage, j.Roots[0].Live); err != nil {
					t.Fatal(err)
				}
			}
			w.candidateDown(t)
			result, err := w.r.RevertManagedData(context.Background(), w.p.OpID)
			if err != nil || result.State != revive.StateRolledBack || read(t, filepath.Join(w.state, "SOUL.md")) != "current soul" ||
				read(t, filepath.Join(j.Roots[1].Live, "old")) != "original extra" {
				t.Fatal("partial exchange did not reconcile all original roots", result, err)
			}
			candidate := j.Roots[0].Stage
			if candidatePlaced {
				candidate = j.Roots[0].Failed
			}
			if read(t, filepath.Join(candidate, "SOUL.md")) != "restored" {
				t.Fatal("failed candidate lost")
			}
		})
	}
}

func TestManagedDataReopensCompletedJournalBeforeRollback(t *testing.T) {
	w := newManagedWorld(t, false)
	w.begin(t)
	w.originalDown(t)
	if _, err := w.r.ExchangeManagedData(context.Background(), w.p.OpID); err != nil {
		t.Fatal(err)
	}
	w.candidateDown(t)
	j, _ := w.r.Store.Load(w.p.OpID)
	if err := w.r.Store.reopenManagedData(j); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(w.r.Store.DoneDir(), w.p.OpID+".json")); !os.IsNotExist(err) {
		t.Fatal("completed journal still masks unfinished rollback")
	}
	// Reconnect after reopening and after the first rollback rename.
	if err := w.r.Store.Set(j, revive.StateRollingBack); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(j.Roots[0].Live, j.Roots[0].Failed); err != nil {
		t.Fatal(err)
	}
	result, err := w.r.RevertManagedData(context.Background(), w.p.OpID)
	if err != nil || result.State != revive.StateRolledBack || read(t, filepath.Join(w.state, "SOUL.md")) != "current soul" {
		t.Fatal(result, err)
	}
}

func TestManagedDataRefusesLostOriginalAndUnboundStageLink(t *testing.T) {
	t.Run("original-directory-replaced", func(t *testing.T) {
		w := newManagedWorld(t, false)
		w.begin(t)
		w.originalDown(t)
		if _, err := w.r.ExchangeManagedData(context.Background(), w.p.OpID); err != nil {
			t.Fatal(err)
		}
		w.candidateDown(t)
		j, _ := w.r.Store.Load(w.p.OpID)
		if err := os.Rename(j.Roots[0].Prev, j.Roots[0].Prev+"-moved-externally"); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(j.Roots[0].Prev, "SOUL.md"), "wrong directory", 0600)
		if _, err := w.r.RevertManagedData(context.Background(), w.p.OpID); err == nil || read(t, filepath.Join(w.state, "SOUL.md")) != "restored" {
			t.Fatal("missing original was reported as restored", err)
		}
	})
	t.Run("link-not-in-frozen-manifest", func(t *testing.T) {
		w := newManagedWorld(t, false)
		w.begin(t)
		w.originalDown(t)
		j, _ := w.r.Store.Load(w.p.OpID)
		if err := os.Symlink("SOUL.md", filepath.Join(j.Roots[0].Stage, "unbound")); err != nil {
			t.Fatal(err)
		}
		if _, err := w.r.ExchangeManagedData(context.Background(), w.p.OpID); err == nil || read(t, filepath.Join(w.state, "SOUL.md")) != "current soul" {
			t.Fatal("unbound stage link reached live data", err)
		}
	})
}

func TestManagedDataRejectsChangedOperationRevisionAndPlan(t *testing.T) {
	for _, change := range []string{"operation", "vault", "original", "candidate", "plan", "root"} {
		t.Run(change, func(t *testing.T) {
			w := newManagedWorld(t, false)
			w.begin(t)
			w.originalDown(t)
			j, _ := w.r.Store.Load(w.p.OpID)
			switch change {
			case "operation":
				j.ManagedData.OperationID = "other"
			case "vault":
				j.ManagedData.Snapshot.VaultID = "another-vault"
			case "original":
				j.ManagedData.Original.Revision++
			case "candidate":
				j.ManagedData.Candidate.Revision++
			case "plan":
				j.Plan.Carry = true
			case "root":
				j.Roots[0].Live = filepath.Join(w.home, "another")
			}
			if err := w.r.Store.Save(j); err != nil {
				t.Fatal(err)
			}
			if _, err := w.r.ExchangeManagedData(context.Background(), w.p.OpID); err == nil || read(t, filepath.Join(w.state, "SOUL.md")) != "current soul" {
				t.Fatal("changed operation executed", err)
			}
		})
	}
}
