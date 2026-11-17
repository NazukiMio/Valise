package backuprun

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	cp "valise/core/protection"
	"valise/core/vaultrpc"
	porterenroll "valise/porter/internal/enroll"
	"valise/porter/internal/seal"
)

func setPlanCopies(t *testing.T, s Store, agentRefID string, ids ...string) {
	t.Helper()
	path := filepath.Join(s.Home, ".config/valise/protection/demo.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var plan cp.ServerPlan
	if err := json.Unmarshal(b, &plan); err != nil {
		t.Fatal(err)
	}
	yes := true
	plan.ServerCopies = nil
	for _, id := range ids {
		plan.ServerCopies = append(plan.ServerCopies, cp.ServerCopy{
			TargetRef:       cp.LocationRef{Kind: "legacy_target", ID: id, AuthorityID: agentRefID},
			Enabled:         &yes,
			MinimumInterval: "1h",
		})
	}
	put(t, path, plan)
}

func TestCancelInterruptsStalledCopyTarget(t *testing.T) {
	for _, detached := range []bool{false, true} {
		name := "attached"
		if detached {
			name = "detached"
		}
		t.Run(name, func(t *testing.T) {
			s, p := fixture(t)
			setPlanCopies(t, s, p.AgentRef.ServerUID, "nas", "second")
			if detached {
				fakeSystemdRun(t, &s, 0)
				p.Detach = true
			}

			started := make(chan string, 2)
			ctxObserved := make(chan error, 1)
			s.syncFn = func(ctx context.Context, _ *porterenroll.Env, _ seal.Paths, target, _ string) (*vaultrpc.SyncResult, error) {
				started <- target
				select {
				case <-ctx.Done():
					ctxObserved <- ctx.Err()
					return nil, ctx.Err()
				case <-time.After(5 * time.Second):
					return nil, errors.New("sync context was not cancelled")
				}
			}

			ctx := context.Background()
			if detached {
				accepted, err := s.Run(ctx, p)
				if err != nil || accepted.State != "accepted" {
					t.Fatalf("accept detached: %+v %v", accepted, err)
				}
			}

			type runOut struct {
				run *Run
				err error
			}
			done := make(chan runOut, 1)
			go func() {
				var r *Run
				var err error
				if detached {
					r, err = s.Execute(ctx, p.RunID)
				} else {
					r, err = s.Run(ctx, p)
				}
				done <- runOut{r, err}
			}()

			select {
			case gotTarget := <-started:
				if gotTarget != "nas" {
					t.Fatalf("unexpected first copy target %q", gotTarget)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("timed out waiting for copy target to start")
			}

			cancelAt := time.Now()
			ack, err := s.Cancel(ctx, CancelParams{AgentRef: p.AgentRef, RunID: p.RunID})
			if err != nil || ack.CancelRequestedAt == nil {
				t.Fatalf("cancel ack %+v %v", ack, err)
			}

			var out runOut
			select {
			case out = <-done:
			case <-time.After(time.Second):
				t.Fatal("copy cancellation took longer than 1s")
			}
			if elapsed := time.Since(cancelAt); elapsed >= time.Second {
				t.Fatalf("cancel latency %v >= 1s", elapsed)
			}
			select {
			case cerr := <-ctxObserved:
				if !errors.Is(cerr, context.Canceled) {
					t.Fatalf("unexpected copy ctx err: %v", cerr)
				}
			default:
				t.Fatal("syncFn did not observe ctx.Done()")
			}
			select {
			case extra := <-started:
				t.Fatalf("second target %q started after cancellation", extra)
			default:
			}

			r, err := out.run, out.err
			if err != nil || r.State != "cancelled" || r.ErrorCode != "cancelled" || r.Stage != "" || r.StageTarget != nil || r.CancelRequestedAt == nil {
				t.Fatalf("cancelled run %+v %v", r, err)
			}
			if r.Snapshot == nil || snapshotCount(t, s) != 1 {
				t.Fatalf("snapshot lost on cancel: %+v", r)
			}
			if len(r.Components) != 3 {
				t.Fatalf("components %+v", r.Components)
			}
			if r.Components[0].Kind != "server_backup" || r.Components[0].State != "succeeded" {
				t.Fatalf("backup component %+v", r.Components[0])
			}
			c1 := r.Components[1]
			if c1.Kind != "server_copy" || c1.TargetRef == nil || c1.TargetRef.ID != "nas" || c1.State != "cancelled" || c1.ErrorCode != "cancelled" || c1.FailureCause != "" || c1.StartedAt == nil || c1.CompletedAt == nil {
				t.Fatalf("interrupted copy component %+v", c1)
			}
			c2 := r.Components[2]
			if c2.Kind != "server_copy" || c2.TargetRef == nil || c2.TargetRef.ID != "second" || c2.State != "skipped" || c2.ErrorCode != "cancelled" {
				t.Fatalf("remaining copy component %+v", c2)
			}

			bind, err := s.SnapshotBinding(ctx, cp.SnapshotBindingParams{SnapshotRef: *r.Snapshot})
			if err != nil || bind.State != "bound" || bind.AgentRef == nil || *bind.AgentRef != p.AgentRef {
				t.Fatalf("binding %+v %v", bind, err)
			}
			status, err := s.Status(ctx, StatusParams{AgentRef: p.AgentRef, RunID: p.RunID})
			if err != nil || status.State != "cancelled" || status.Snapshot == nil || *status.Snapshot != *r.Snapshot {
				t.Fatalf("status %+v %v", status, err)
			}
		})
	}
}

func TestCancelInterruptsRealHangingSubprocessInStageCopying(t *testing.T) {
	s, p := fixture(t)
	if err := seal.SetServerName(seal.Paths{Home: s.Home}, "box"); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(s.Home, ".config/valise/targets.json"), map[string]any{
		"targets": []vaultrpc.Target{{Name: "nas", Kind: "rsync", Dest: "nas:/tmp/valise-unused"}},
	})
	setPlanCopies(t, s, p.AgentRef.ServerUID, "nas")

	bin := t.TempDir()
	meta := t.TempDir()
	childPIDFile := filepath.Join(meta, "child.pid")
	startedFile := filepath.Join(meta, "started")
	sftpTrapFile := filepath.Join(meta, "sftp-called")

	rsyncScript := "#!/bin/sh\nsleep 60 &\necho $! > '" + childPIDFile + "'\n: > '" + startedFile + "'\nwait\n"
	if err := os.WriteFile(filepath.Join(bin, "rsync"), []byte(rsyncScript), 0700); err != nil {
		t.Fatal(err)
	}
	sftpScript := "#!/bin/sh\n: > '" + sftpTrapFile + "'\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "sftp"), []byte(sftpScript), 0700); err != nil {
		t.Fatal(err)
	}
	s.Env.Vars = []string{"PATH=" + bin + ":/usr/bin:/bin"}

	type runOut struct {
		run *Run
		err error
	}
	done := make(chan runOut, 1)
	go func() {
		r, err := s.Run(context.Background(), p)
		done <- runOut{r, err}
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(startedFile); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for fake rsync subprocess to start")
		}
		time.Sleep(10 * time.Millisecond)
	}

	rawPID, err := os.ReadFile(childPIDFile)
	if err != nil {
		t.Fatal(err)
	}
	childPID, err := strconv.Atoi(strings.TrimSpace(string(rawPID)))
	if err != nil || childPID <= 0 {
		t.Fatalf("invalid child pid %q: %v", rawPID, err)
	}
	t.Cleanup(func() { _ = syscall.Kill(childPID, syscall.SIGKILL) })

	cancelAt := time.Now()
	if _, err := s.Cancel(context.Background(), CancelParams{AgentRef: p.AgentRef, RunID: p.RunID}); err != nil {
		t.Fatal(err)
	}

	var out runOut
	select {
	case out = <-done:
	case <-time.After(time.Second):
		t.Fatal("real subprocess copy cancellation took longer than 1s")
	}
	if time.Since(cancelAt) >= time.Second {
		t.Fatalf("cancel took %v >= 1s", time.Since(cancelAt))
	}
	if _, err := os.Stat(sftpTrapFile); !os.IsNotExist(err) {
		t.Fatal("sftp fallback was spawned after copyCtx cancellation")
	}
	r, err := out.run, out.err
	if err != nil || r.State != "cancelled" || r.ErrorCode != "cancelled" || r.Snapshot == nil {
		t.Fatalf("cancelled run %+v %v", r, err)
	}
	if len(r.Components) != 2 || r.Components[0].State != "succeeded" || r.Components[1].State != "cancelled" || r.Components[1].ErrorCode != "cancelled" {
		t.Fatalf("components %+v", r.Components)
	}

	reapDeadline := time.Now().Add(500 * time.Millisecond)
	for {
		if err := syscall.Kill(childPID, 0); errors.Is(err, syscall.ESRCH) {
			break
		}
		if time.Now().After(reapDeadline) {
			t.Fatalf("subprocess group child pid %d still alive after cancel", childPID)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCancelCompletedCopyAndParentCtxCancelBehavior(t *testing.T) {
	t.Run("parent ctx cancel without cancel request is partial not cancelled", func(t *testing.T) {
		s, p := fixture(t)
		setPlanCopies(t, s, p.AgentRef.ServerUID, "nas")
		started := make(chan struct{}, 1)
		s.syncFn = func(ctx context.Context, _ *porterenroll.Env, _ seal.Paths, _, _ string) (*vaultrpc.SyncResult, error) {
			started <- struct{}{}
			<-ctx.Done()
			return nil, ctx.Err()
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan *Run, 1)
		go func() {
			r, err := s.Run(ctx, p)
			if err != nil {
				t.Error(err)
			}
			done <- r
		}()
		<-started
		cancel()
		select {
		case r := <-done:
			if r.State != "partial" || r.Snapshot == nil || len(r.Components) != 2 || r.Components[1].State != "failed" {
				t.Fatalf("unexpected run on parent cancel without request: %+v", r)
			}
		case <-time.After(time.Second):
			t.Fatal("run did not stop when parent ctx cancelled")
		}
	})

	t.Run("copy succeeding as cancel arrives keeps succeeded copy and skips remaining", func(t *testing.T) {
		s, p := fixture(t)
		setPlanCopies(t, s, p.AgentRef.ServerUID, "one", "two")
		s.syncFn = func(_ context.Context, _ *porterenroll.Env, _ seal.Paths, target, _ string) (*vaultrpc.SyncResult, error) {
			if target != "one" {
				t.Fatalf("unexpected second copy %q", target)
			}
			if err := s.requestCancel(p.RunID); err != nil {
				return nil, err
			}
			return &vaultrpc.SyncResult{Target: target}, nil
		}
		r, err := s.Run(context.Background(), p)
		if err != nil || r.State != "cancelled" || r.Snapshot == nil || len(r.Components) != 3 {
			t.Fatalf("unexpected run %+v %v", r, err)
		}
		if r.Components[1].State != "succeeded" || r.Components[2].State != "skipped" || r.Components[2].ErrorCode != "cancelled" {
			t.Fatalf("unexpected components %+v", r.Components)
		}
	})

	t.Run("no goroutine leak from runCopy", func(t *testing.T) {
		s, p := fixture(t)
		s.syncFn = func(context.Context, *porterenroll.Env, seal.Paths, string, string) (*vaultrpc.SyncResult, error) {
			return &vaultrpc.SyncResult{Target: "one"}, nil
		}
		paths := seal.Paths{Home: s.Home}
		if _, cancelled := s.runCopy(context.Background(), p.RunID, paths, "one"); cancelled {
			t.Fatal("unexpected cancel")
		}
		runtime.GC()
		before := runtime.NumGoroutine()
		for i := range 20 {
			if _, cancelled := s.runCopy(context.Background(), strings.Repeat(strconv.Itoa(i%10), 32), paths, "one"); cancelled {
				t.Fatal("unexpected cancel")
			}
		}
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			runtime.GC()
			if runtime.NumGoroutine() <= before {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("goroutine count grew: before=%d after=%d", before, runtime.NumGoroutine())
	})
}

func TestEnvRunKillsProcessGroupAndHonorsWaitDelay(t *testing.T) {
	t.Run("kills child in same process group promptly", func(t *testing.T) {
		home := t.TempDir()
		env := &porterenroll.Env{Home: home, Vars: []string{"PATH=/usr/bin:/bin"}}
		pidFile := filepath.Join(home, "child.pid")
		readyFile := filepath.Join(home, "ready")
		script := filepath.Join(home, "hang.sh")
		content := "#!/bin/sh\nsleep 60 &\necho $! > '" + pidFile + "'\n: > '" + readyFile + "'\nwait\n"
		if err := os.WriteFile(script, []byte(content), 0700); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		var wg sync.WaitGroup
		var runErr error
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, runErr = env.Run(ctx, time.Minute, home, script)
		}()

		deadline := time.Now().Add(3 * time.Second)
		for {
			if _, err := os.Stat(readyFile); err == nil {
				break
			}
			if time.Now().After(deadline) {
				cancel()
				t.Fatal("timed out waiting for child process")
			}
			time.Sleep(10 * time.Millisecond)
		}
		raw, err := os.ReadFile(pidFile)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
		if err != nil || pid <= 0 {
			cancel()
			t.Fatalf("bad pid %q: %v", raw, err)
		}
		t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

		start := time.Now()
		cancel()
		wg.Wait()
		if !errors.Is(runErr, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", runErr)
		}
		if elapsed := time.Since(start); elapsed >= time.Second {
			t.Fatalf("Env.Run took %v after cancel", elapsed)
		}
		reapDeadline := time.Now().Add(500 * time.Millisecond)
		for {
			if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
				break
			}
			if time.Now().After(reapDeadline) {
				t.Fatalf("grandchild pid %d survived process group kill", pid)
			}
			time.Sleep(10 * time.Millisecond)
		}
	})

	t.Run("wait delay bounds open pipes from detached grandchild", func(t *testing.T) {
		home := t.TempDir()
		env := &porterenroll.Env{
			Home: home,
			Vars: []string{
				"PATH=/usr/bin:/bin",
				"GO_WANT_DETACHED_PIPE_HELPER=1",
				"VALISE_PIPE_PID_FILE=" + filepath.Join(home, "detached.pid"),
				"VALISE_PIPE_READY=" + filepath.Join(home, "ready"),
			},
		}
		ctx, cancel := context.WithCancel(context.Background())
		var wg sync.WaitGroup
		var runErr error
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, runErr = env.Run(ctx, time.Minute, home, os.Args[0], "-test.run=^TestDetachedPipeHelperProcess$")
		}()

		deadline := time.Now().Add(3 * time.Second)
		for {
			if _, err := os.Stat(filepath.Join(home, "ready")); err == nil {
				break
			}
			if time.Now().After(deadline) {
				cancel()
				t.Fatal("timed out waiting for detached helper")
			}
			time.Sleep(10 * time.Millisecond)
		}
		raw, err := os.ReadFile(filepath.Join(home, "detached.pid"))
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
		if err != nil || pid <= 0 {
			cancel()
			t.Fatalf("bad detached pid %q: %v", raw, err)
		}
		t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

		start := time.Now()
		cancel()
		wg.Wait()
		elapsed := time.Since(start)
		if !errors.Is(runErr, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", runErr)
		}
		if elapsed < 1500*time.Millisecond || elapsed >= 4*time.Second {
			t.Fatalf("expected WaitDelay (~2s) to bound return before 8s grandchild sleep, got %v", elapsed)
		}
	})
}

func TestDetachedPipeHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_DETACHED_PIPE_HELPER") != "1" {
		return
	}
	cmd := exec.Command("/bin/sh", "-c", "exec sleep 8")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		os.Exit(2)
	}
	_ = os.WriteFile(os.Getenv("VALISE_PIPE_PID_FILE"), []byte(strconv.Itoa(cmd.Process.Pid)), 0600)
	_ = os.WriteFile(os.Getenv("VALISE_PIPE_READY"), nil, 0600)
	time.Sleep(10 * time.Second)
	os.Exit(0)
}
