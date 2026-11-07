package backuprun

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"

	cp "valise/core/protection"
	"valise/core/valise"
	planstore "valise/porter/internal/protection"
)

// CancelParams names the run whose executor should stop at a safe point.
type CancelParams = cp.BackupCancelParams

// launchGrace is how long an accepted detached run may wait for its unit to
// take the run lock before a read reports the result as unknown.
const launchGrace = time.Minute

// lockPoll is the interval for waiting on locks held by another executor.
var lockPoll = 200 * time.Millisecond

// runLockWindow outlasts momentary probes of a run lock.
var runLockWindow = 2 * time.Second

func (s Store) runLockPath(id string) string { return filepath.Join(s.dir(), id+".lock") }
func (s Store) cancelPath(id string) string  { return filepath.Join(s.dir(), id+".cancel") }

// runLock is held by the one executor of a run for as long as it lives, so
// readers can tell a live run from one whose process ended. Status probes
// and cancel requests hold it only for an instant, so a short retry window
// separates them from another live executor.
func (s Store) runLock(id string) (func(), error) { return s.runLockWithin(id, runLockWindow) }
func (s Store) runLockWithin(id string, window time.Duration) (func(), error) {
	f, err := os.OpenFile(s.runLockPath(id), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(window)
	for syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		if time.Now().After(deadline) {
			f.Close()
			return nil, failure("resource_busy")
		}
		time.Sleep(lockPoll / 4)
	}
	return func() { f.Close() }, nil
}

// live reports whether an executor holds the run lock. It never creates or
// changes a file.
func (s Store) live(id string) bool {
	f, err := os.OpenFile(s.runLockPath(id), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return false
	}
	defer f.Close()
	if syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB) != nil {
		return true
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}

func (s Store) cancelRequested(id string) *time.Time {
	st, err := os.Lstat(s.cancelPath(id))
	if err != nil || !st.Mode().IsRegular() {
		return nil
	}
	at := st.ModTime().UTC()
	return &at
}

// project is the read view of a durable record. Only a live executor (or a
// detached unit still within its start grace) keeps accepted/running;
// anything else is reported as unknown and is never retried.
func (s Store) project(r *Run) *Run {
	r.CancelRequestedAt = s.cancelRequested(r.RunID)
	switch r.State {
	case "accepted", "running":
	default:
		return r
	}
	if s.live(r.RunID) {
		return r
	}
	if r.State == "accepted" && r.Execution == cp.ExecutionDetached && time.Since(r.UpdatedAt) < launchGrace {
		return r
	}
	r.State = "reconciling"
	r.ErrorCode = "result_unknown"
	return r
}

// waitLock waits for the store lock, which an executor holds for a whole run.
func (s Store) waitLock(ctx context.Context) (func(), error) {
	for {
		unlock, err := s.lock()
		if err == nil {
			return unlock, nil
		}
		if ue, ok := valise.As(err); !ok || ue.Code != "resource_busy" {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(lockPoll):
		}
	}
}

// Execute is the task-run entry: it executes only the request frozen in the
// accepted record and claims it at most once. A run that is already running,
// owned by another live executor or finished is returned unchanged.
func (s Store) Execute(ctx context.Context, id string) (*Run, error) {
	if !cp.ValidID(id) {
		return nil, failure("request_invalid")
	}
	if s.Env == nil {
		return nil, failure("environment_unavailable")
	}
	if _, err := s.load(id); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, failure("run_not_found")
		}
		return nil, err
	}
	release, err := s.runLock(id)
	if err != nil {
		r, lerr := s.load(id)
		if lerr != nil {
			return nil, lerr
		}
		return s.project(r), nil
	}
	defer release()
	unlock, err := s.waitLock(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	r, err := s.load(id)
	if err != nil {
		return nil, err
	}
	if r.State != "accepted" {
		// Claimed earlier by an executor that has ended: this process holds
		// the run lock itself, so the read view must not count it as live.
		if r.State == "running" {
			r.State = "reconciling"
			r.ErrorCode = "result_unknown"
		}
		r.CancelRequestedAt = s.cancelRequested(id)
		return r, nil
	}
	if s.stop(r, cp.StageQueued) {
		s.markCancelled(r)
		return r, s.persist(r)
	}
	ps := planstore.Store{Home: s.Home, Env: s.Env, ValidateAgent: s.ValidateAgent}
	entered := false
	for {
		err = ps.WithAccepted(ctx, r.AgentRef, func(plan *cp.ServerPlan) error {
			entered = true
			got, _ := cp.Digest(plan)
			frozen, _ := cp.Digest(r.Plan)
			if plan.Revision != r.Plan.Revision || got != frozen {
				// Nothing was sealed: the accepted plan changed before start.
				r.State = "failed"
				r.ErrorCode = "revision_conflict"
				r.Stage = ""
				return s.persist(r)
			}
			return s.execute(ctx, r)
		})
		if entered || err == nil {
			break
		}
		if ue, ok := valise.As(err); !ok || ue.Code != "resource_busy" {
			break
		}
		select {
		case <-ctx.Done():
			err = ctx.Err()
		case <-time.After(lockPoll):
			continue
		}
		break
	}
	if err != nil && !entered {
		// The plan could not be frozen again; no side effect happened.
		code := "interrupted"
		if ue, ok := valise.As(err); ok {
			code = ue.Code
		}
		r.State = "failed"
		r.ErrorCode = code
		r.Stage = ""
		if perr := s.persist(r); perr != nil {
			return nil, perr
		}
		return r, err
	}
	return r, err
}

// Cancel records a cancel request for the executor's next safe point. It
// never kills a process; a run that has not been claimed yet is cancelled
// directly because no executor can claim it while the locks are held here.
func (s Store) Cancel(ctx context.Context, p CancelParams) (*Run, error) {
	if !cp.ValidID(p.RunID) {
		return nil, failure("request_invalid")
	}
	if err := s.validate(ctx, p.AgentRef); err != nil {
		return nil, err
	}
	r, err := s.load(p.RunID)
	if errors.Is(err, os.ErrNotExist) {
		return nil, failure("run_not_found")
	}
	if err != nil {
		return nil, err
	}
	if r.AgentRef != p.AgentRef {
		return nil, failure("identity_conflict")
	}
	switch r.State {
	case "accepted", "running":
	default:
		return s.project(r), nil
	}
	if err := s.requestCancel(p.RunID); err != nil {
		return nil, err
	}
	if err := s.cancelUnclaimed(p.RunID); err != nil {
		return nil, err
	}
	r, err = s.load(p.RunID)
	if err != nil {
		return nil, err
	}
	return s.project(r), nil
}

// cancelUnclaimed marks an accepted run cancelled when no executor is alive.
// Holding both locks keeps any unit from claiming it meanwhile; when either
// is busy the request stays for the executor's next safe point.
func (s Store) cancelUnclaimed(id string) error {
	release, err := s.runLockWithin(id, 0)
	if err != nil {
		return nil
	}
	defer release()
	unlock, err := s.lock()
	if err != nil {
		return nil
	}
	defer unlock()
	r, err := s.load(id)
	if err != nil {
		return err
	}
	if r.State != "accepted" || !s.stop(r, cp.StageQueued) {
		return nil
	}
	s.markCancelled(r)
	return s.persist(r)
}

func (s Store) requestCancel(id string) error {
	f, err := os.OpenFile(s.cancelPath(id), os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	d, err := os.Open(s.dir())
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// stop is the safe-point check. Before sealing nothing has happened; after
// the snapshot was committed it is kept and bound, and the copies that had
// not started are recorded as skipped. stage names the safe point.
func (s Store) stop(r *Run, stage string) bool {
	if s.hook != nil {
		s.hook(stage)
	}
	return s.cancelRequested(r.RunID) != nil
}

// markCancelled records a verified stop. The executor has passed no further
// side effect since the safe point that observed the request.
func (s Store) markCancelled(r *Run) {
	r.State = "cancelled"
	r.ErrorCode = "cancelled"
	r.Stage = ""
	r.StageTarget = nil
	r.CancelRequestedAt = s.cancelRequested(r.RunID)
}
