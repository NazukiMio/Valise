package revive

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"valise/core/environment"
	"valise/core/revive"
)

func testPlan(w *world) revive.Plan {
	p := w.plan()
	p.TestOnly, p.Carry, p.Unit = true, false, ""
	p.Body = revive.BodyPlan{Action: revive.BodyKeep}
	return p
}

func TestTestOnlyJournalCannotSwapOrContinue(t *testing.T) {
	w := newWorld(t)
	p := testPlan(w)
	w.stage(t, p, snapshot)
	if _, err := w.r.Swap(context.Background(), p.OpID); err == nil {
		t.Fatal("test journal swapped live state")
	}
	if _, err := w.r.Resume(context.Background(), revive.ResumeParams{OpID: p.OpID, Action: "continue"}); err == nil {
		t.Fatal("test journal continued into activation")
	}
	if got := read(t, filepath.Join(w.state, "SOUL.md")); got != "current soul" {
		t.Fatal("test replaced active data")
	}
	if _, err := w.r.Resume(context.Background(), revive.ResumeParams{OpID: p.OpID, Action: "back"}); err != nil {
		t.Fatal(err)
	}
	j, err := w.r.Store.Load(p.OpID)
	if err != nil || j.State != revive.StateRolledBack {
		t.Fatal("test did not finish by discarding", err)
	}
	for _, root := range j.Roots {
		if _, err := os.Lstat(root.Stage); !os.IsNotExist(err) {
			t.Fatal("test back kept staging data")
		}
	}
}

func TestTestOnlyPlanRefusesExecutableRestoreSteps(t *testing.T) {
	for _, change := range []func(*revive.Plan){
		func(p *revive.Plan) { p.Carry = true },
		func(p *revive.Plan) { p.Unit = "fixture.service" },
		func(p *revive.Plan) { p.InstallService = true },
		func(p *revive.Plan) { p.Body.Action = revive.BodyNPMInstall },
		func(p *revive.Plan) { p.Keep = []string{"state/another"} },
		func(p *revive.Plan) { p.Supersedes = []string{"another"} },
		func(p *revive.Plan) { p.TestOnly = false; p.IsolatedTest = &environment.TestInput{} },
	} {
		w := newWorld(t)
		p := testPlan(w)
		change(&p)
		if _, err := StageBegin(context.Background(), w.r.Env, w.r.Store, p); err == nil {
			t.Fatal("test admitted active restore steps")
		}
	}
}

func TestEnvironmentCleanupRequiresStoppedProofAndOriginalPrivateDirectory(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		w := newWorld(t)
		p := testPlan(w)
		p.IsolatedTest = &environment.TestInput{}
		w.stage(t, p, snapshot)
		parent, err := w.r.testDirectory(p.OpID, true)
		if err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(parent, "candidate"), "temporary", 0600)
		j, _ := w.r.Store.Load(p.OpID)
		j.EnvironmentTest = &environment.TestResult{State: "probing", ProbeAttempted: true}
		if !unknown {
			j.EnvironmentTest.State = "finished"
			j.EnvironmentTest.Probe = environment.ProbeResult{Completed: true, ProcessesStopped: true}
		}
		if err := w.r.Store.Save(j); err != nil {
			t.Fatal(err)
		}
		_, err = w.r.Resume(context.Background(), revive.ResumeParams{OpID: p.OpID, Action: "discard"})
		if unknown {
			if err == nil || read(t, filepath.Join(parent, "candidate")) != "temporary" {
				t.Fatal("unknown running candidate was deleted")
			}
		} else if err != nil {
			t.Fatal(err)
		} else if _, err := os.Lstat(parent); !os.IsNotExist(err) {
			t.Fatal("stopped candidate not cleaned")
		}
		if read(t, filepath.Join(w.state, "SOUL.md")) != "current soul" {
			t.Fatal("cleanup touched live state")
		}
	}
}
