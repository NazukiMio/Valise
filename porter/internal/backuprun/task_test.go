package backuprun

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cp "valise/core/protection"
	"valise/porter/internal/seal"
)

// fakeSystemdRun installs a systemd-run stand-in on the Env's PATH. It only
// records its argv; the test plays the unit by calling Execute.
func fakeSystemdRun(t *testing.T, s *Store, code int) string {
	t.Helper()
	bin := t.TempDir()
	args := filepath.Join(t.TempDir(), "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + args + "'\nexit " + map[int]string{0: "0", 1: "1"}[code] + "\n"
	if err := os.WriteFile(filepath.Join(bin, "systemd-run"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	s.Env.Vars = []string{"PATH=" + bin + ":/usr/bin:/bin"}
	s.Launch = SystemdLauncher(s.Env, s.Home, "/opt/valise/valise-porter")
	return args
}

func snapshotCount(t *testing.T, s Store) int {
	t.Helper()
	st, err := seal.Status(seal.Paths{Home: s.Home})
	if err != nil {
		t.Fatal(err)
	}
	return len(st.Snapshots)
}

func TestDetachedLaunchAndTaskRunNeverReplays(t *testing.T) {
	s, p := fixture(t)
	argsFile := fakeSystemdRun(t, &s, 0)
	p.Detach = true
	ctx := context.Background()
	r, err := s.Run(ctx, p)
	if err != nil || r.State != "accepted" || r.Execution != cp.ExecutionDetached || r.Stage != cp.StageQueued || r.Snapshot != nil {
		t.Fatalf("detached %+v %v", r, err)
	}
	b, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join(LaunchArgs("/opt/valise/valise-porter", p.RunID)[1:], "\n") + "\n"
	if string(b) != want || !strings.Contains(want, "--property=NoNewPrivileges=yes") || strings.Contains(want, "Capabilit") {
		t.Fatalf("argv %q", b)
	}
	if snapshotCount(t, s) != 0 {
		t.Fatal("detached run sealed inside the connection")
	}
	// Within the launch grace an accepted detached run is not unknown.
	st, err := s.Status(ctx, StatusParams{AgentRef: p.AgentRef, RunID: p.RunID})
	if err != nil || st.State != "accepted" {
		t.Fatalf("status %+v %v", st, err)
	}
	// The unit: executes only the frozen request.
	done, err := s.Execute(ctx, p.RunID)
	if err != nil || done.State != "succeeded" || done.Snapshot == nil || done.Stage != "" || done.Execution != cp.ExecutionDetached {
		t.Fatalf("task-run %+v %v", done, err)
	}
	// A second start of the same unit, and a client retry, replay nothing.
	again, err := s.Execute(ctx, p.RunID)
	if err != nil || again.State != "succeeded" || *again.Snapshot != *done.Snapshot {
		t.Fatalf("rerun %+v %v", again, err)
	}
	retry, err := s.Run(ctx, p)
	if err != nil || retry.State != "succeeded" || *retry.Snapshot != *done.Snapshot {
		t.Fatalf("retry %+v %v", retry, err)
	}
	if snapshotCount(t, s) != 1 {
		t.Fatal("run replayed")
	}
	if _, err := s.Execute(ctx, strings.Repeat("e", 32)); err == nil {
		t.Fatal("unknown run executed")
	}
	if _, err := s.Execute(ctx, "../x"); err == nil {
		t.Fatal("invalid id accepted")
	}
}

func TestLaunchFailureExecutesAttached(t *testing.T) {
	for _, mode := range []string{"fails", "missing"} {
		t.Run(mode, func(t *testing.T) {
			s, p := fixture(t)
			fakeSystemdRun(t, &s, 1)
			p.Detach = true
			if mode == "missing" {
				s.Env.Vars = []string{"PATH=" + t.TempDir()}
			}
			r, err := s.Run(context.Background(), p)
			if err != nil || r.State != "succeeded" || r.Execution != cp.ExecutionAttached || r.Snapshot == nil {
				t.Fatalf("attached %+v %v", r, err)
			}
			durable, err := s.load(p.RunID)
			if err != nil || durable.Execution != cp.ExecutionAttached {
				t.Fatalf("durable %+v %v", durable, err)
			}
			// A late unit start finds the run claimed and does nothing.
			if again, err := s.Execute(context.Background(), p.RunID); err != nil || again.State != "succeeded" || snapshotCount(t, s) != 1 {
				t.Fatalf("late unit %+v %v", again, err)
			}
		})
	}
}

func TestLiveExecutorIsRunningAndDuplicateUnitExits(t *testing.T) {
	s, p := fixture(t)
	fakeSystemdRun(t, &s, 0)
	p.Detach = true
	ctx := context.Background()
	if _, err := s.Run(ctx, p); err != nil {
		t.Fatal(err)
	}
	r, _ := s.load(p.RunID)
	r.State = "running"
	r.UpdatedAt = time.Now().Add(-time.Hour)
	if err := s.save(r); err != nil {
		t.Fatal(err)
	}
	release, err := s.runLock(p.RunID)
	if err != nil {
		t.Fatal(err)
	}
	old := runLockWindow
	runLockWindow = 50 * time.Millisecond
	defer func() { runLockWindow = old }()
	st, err := s.Status(ctx, StatusParams{AgentRef: p.AgentRef, RunID: p.RunID})
	if err != nil || st.State != "running" {
		t.Fatalf("live %+v %v", st, err)
	}
	dup, err := s.Execute(ctx, p.RunID)
	if err != nil || dup.State != "running" || snapshotCount(t, s) != 0 {
		t.Fatalf("duplicate %+v %v", dup, err)
	}
	release()
	st, err = s.Status(ctx, StatusParams{AgentRef: p.AgentRef, RunID: p.RunID})
	if err != nil || st.State != "reconciling" {
		t.Fatalf("ended %+v %v", st, err)
	}
	// An ended executor's running record is never claimed again.
	if r, err := s.Execute(ctx, p.RunID); err != nil || r.State != "reconciling" || snapshotCount(t, s) != 0 {
		t.Fatalf("replayed %+v %v", r, err)
	}
	// A detached accepted run whose unit never started becomes unknown.
	p.RunID = strings.Repeat("d", 32)
	if _, err := s.Run(ctx, p); err != nil {
		t.Fatal(err)
	}
	r, _ = s.load(p.RunID)
	r.UpdatedAt = time.Now().Add(-2 * launchGrace)
	if err := s.save(r); err != nil {
		t.Fatal(err)
	}
	if st, err := s.Status(ctx, StatusParams{AgentRef: p.AgentRef, RunID: p.RunID}); err != nil || st.State != "reconciling" {
		t.Fatalf("stale accepted %+v %v", st, err)
	}
}

func TestCancelBeforeClaimStopsWithoutSideEffects(t *testing.T) {
	s, p := fixture(t)
	fakeSystemdRun(t, &s, 0)
	p.Detach = true
	ctx := context.Background()
	if _, err := s.Run(ctx, p); err != nil {
		t.Fatal(err)
	}
	r, err := s.Cancel(ctx, CancelParams{AgentRef: p.AgentRef, RunID: p.RunID})
	if err != nil || r.State != "cancelled" || r.CancelRequestedAt == nil || r.Snapshot != nil {
		t.Fatalf("cancel %+v %v", r, err)
	}
	if r, err = s.Execute(ctx, p.RunID); err != nil || r.State != "cancelled" || snapshotCount(t, s) != 0 {
		t.Fatalf("unit after cancel %+v %v", r, err)
	}
	other := p.AgentRef
	other.Generation = "other"
	if _, err := s.Cancel(ctx, CancelParams{AgentRef: other, RunID: p.RunID}); err == nil {
		t.Fatal("foreign agent cancelled")
	}
}

func TestCancelWhileQueuedIsDeferredToTheUnit(t *testing.T) {
	s, p := fixture(t)
	fakeSystemdRun(t, &s, 0)
	p.Detach = true
	ctx := context.Background()
	if _, err := s.Run(ctx, p); err != nil {
		t.Fatal(err)
	}
	// The unit is alive (holds the run lock) but waits for the store lock.
	release, err := s.runLock(p.RunID)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Cancel(ctx, CancelParams{AgentRef: p.AgentRef, RunID: p.RunID})
	if err != nil || r.State != "accepted" || r.CancelRequestedAt == nil {
		t.Fatalf("deferred %+v %v", r, err)
	}
	release()
	if r, err = s.Execute(ctx, p.RunID); err != nil || r.State != "cancelled" || snapshotCount(t, s) != 0 {
		t.Fatalf("unit %+v %v", r, err)
	}
}

func TestCancelAtSafePoints(t *testing.T) {
	t.Run("before sealing", func(t *testing.T) {
		s, p := fixture(t)
		s.hook = func(stage string) {
			if stage == cp.StageSealing {
				if err := s.requestCancel(p.RunID); err != nil {
					t.Fatal(err)
				}
			}
		}
		r, err := s.Run(context.Background(), p)
		if err != nil || r.State != "cancelled" || r.Snapshot != nil || r.Components[0].State != "not_started" || snapshotCount(t, s) != 0 {
			t.Fatalf("cancel %+v %v", r, err)
		}
	})
	t.Run("before copy keeps snapshot", func(t *testing.T) {
		s, p := fixture(t)
		path := filepath.Join(s.Home, ".config/valise/protection/demo.json")
		b, _ := os.ReadFile(path)
		var plan cp.ServerPlan
		if err := json.Unmarshal(b, &plan); err != nil {
			t.Fatal(err)
		}
		yes := true
		for _, id := range []string{"one", "two"} {
			plan.ServerCopies = append(plan.ServerCopies, cp.ServerCopy{TargetRef: cp.LocationRef{Kind: "legacy_target", ID: id, AuthorityID: p.AgentRef.ServerUID}, Enabled: &yes, MinimumInterval: "1h"})
		}
		put(t, path, plan)
		copies := 0
		s.hook = func(stage string) {
			if stage == cp.StageCopying {
				copies++
				if err := s.requestCancel(p.RunID); err != nil {
					t.Fatal(err)
				}
			}
		}
		r, err := s.Run(context.Background(), p)
		if err != nil || r.State != "cancelled" || r.Snapshot == nil || copies != 1 {
			t.Fatalf("cancel %+v %v", r, err)
		}
		if len(r.Components) != 3 || r.Components[0].State != "succeeded" {
			t.Fatalf("components %+v", r.Components)
		}
		for _, c := range r.Components[1:] {
			if c.State != "skipped" || c.ErrorCode != "cancelled" {
				t.Fatalf("copy %+v", c)
			}
		}
		bind, err := s.SnapshotBinding(context.Background(), cp.SnapshotBindingParams{SnapshotRef: *r.Snapshot})
		if err != nil || bind.State != "bound" {
			t.Fatalf("snapshot lost its binding %+v %v", bind, err)
		}
		// Cancelling a finished run changes nothing.
		again, err := s.Cancel(context.Background(), CancelParams{AgentRef: p.AgentRef, RunID: p.RunID})
		if err != nil || again.State != "cancelled" || *again.Snapshot != *r.Snapshot {
			t.Fatalf("again %+v %v", again, err)
		}
	})
}

func TestPlanChangedBeforeUnitStartFails(t *testing.T) {
	s, p := fixture(t)
	fakeSystemdRun(t, &s, 0)
	p.Detach = true
	ctx := context.Background()
	if _, err := s.Run(ctx, p); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.Home, ".config/valise/protection/demo.json")
	b, _ := os.ReadFile(path)
	var plan cp.ServerPlan
	if err := json.Unmarshal(b, &plan); err != nil {
		t.Fatal(err)
	}
	plan.Revision = 2
	put(t, path, plan)
	r, err := s.Execute(ctx, p.RunID)
	if err != nil || r.State != "failed" || r.ErrorCode != "revision_conflict" || snapshotCount(t, s) != 0 {
		t.Fatalf("changed plan %+v %v", r, err)
	}
}

func TestLaunchOnlyForDetachRequests(t *testing.T) {
	s, p := fixture(t)
	argsFile := fakeSystemdRun(t, &s, 0)
	r, err := s.Run(context.Background(), p)
	if err != nil || r.State != "succeeded" || r.Execution != cp.ExecutionAttached {
		t.Fatalf("sync request %+v %v", r, err)
	}
	if _, err := os.Stat(argsFile); !os.IsNotExist(err) {
		t.Fatal("launched a unit for a synchronous request")
	}
	p.RunID = strings.Repeat("f", 32)
	p.Reason = "timer"
	p.Detach = true
	if _, err := s.Run(context.Background(), p); err == nil {
		t.Fatal("timer request detached")
	}
}
