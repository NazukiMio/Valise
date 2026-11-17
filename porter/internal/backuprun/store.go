// Package backuprun executes only an accepted preset protection plan.
package backuprun

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"valise/core/identity"
	cp "valise/core/protection"
	"valise/core/valise"
	"valise/core/vaultrpc"
	porterenroll "valise/porter/internal/enroll"
	planstore "valise/porter/internal/protection"
	"valise/porter/internal/seal"
)

type Params = cp.BackupParams
type StatusParams = cp.BackupStatusParams
type Run = cp.BackupRun

type Store struct {
	Home          string
	Env           *porterenroll.Env
	ValidateAgent func(context.Context, identity.AgentRef) error
	// Launch starts `task-run --id <run_id>` outside this process (a
	// transient systemd user unit). Nil, or a failed launch, executes the
	// accepted run attached to the caller. It is used only for a request
	// with Detach; the timer path leaves it nil.
	Launch func(ctx context.Context, runID string) error

	// hook is a test seam called at each executor safe point.
	hook func(stage string)

	// syncFn is an optional package-internal test seam replacing seal.Sync.
	syncFn func(ctx context.Context, env *porterenroll.Env, paths seal.Paths, name, initiator string) (*vaultrpc.SyncResult, error)
}

func failure(code string) error {
	return valise.New(code, "备份运行记录或执行条件未能核实", "重新读取保护计划和运行状态；结果未知时请勿重放备份")
}
func (s Store) dir() string           { return filepath.Join(s.Home, ".local", "state", "valise", "runs") }
func (s Store) path(id string) string { return filepath.Join(s.dir(), id+".json") }
func (s Store) validate(ctx context.Context, a identity.AgentRef) error {
	if err := cp.ValidateAgent(a); err != nil {
		return err
	}
	if s.ValidateAgent == nil {
		return failure("identity_unbound")
	}
	return s.ValidateAgent(ctx, a)
}
func (s Store) lock() (func(), error) {
	if err := os.MkdirAll(s.dir(), 0700); err != nil {
		return nil, err
	}
	st, err := os.Lstat(s.dir())
	if err != nil {
		return nil, err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return nil, failure("record_corrupt")
	}
	if err = os.Chmod(s.dir(), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(s.dir(), "runs.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, failure("resource_busy")
	}
	return func() { f.Close() }, nil
}
func (s Store) save(r *Run) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(s.dir(), ".run-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(append(b, '\n'))
	}
	if err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err == nil {
		err = ce
	}
	if err != nil {
		return err
	}
	if err = os.Rename(f.Name(), s.path(r.RunID)); err != nil {
		return err
	}
	d, err := os.Open(s.dir())
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
func (s Store) load(id string) (*Run, error) {
	st, err := os.Lstat(s.path(id))
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 {
		return nil, failure("record_corrupt")
	}
	b, err := os.ReadFile(s.path(id))
	if err != nil {
		return nil, err
	}
	var r Run
	if json.Unmarshal(b, &r) != nil {
		return nil, failure("record_corrupt")
	}
	p, err := r.Request.Normalize()
	if err != nil {
		return nil, failure("record_corrupt")
	}
	digest, _ := cp.Digest(p)
	if r.Schema != cp.BackupRunSchema || r.RunID != id || r.Request != p || p.RunID != id || r.AgentRef != p.AgentRef || r.RequestDigest != digest || r.Plan.AgentRef != r.AgentRef || r.Plan.Revision != p.ExpectedRevision || r.Plan.Validate() != nil || r.AcceptedAt.IsZero() || r.UpdatedAt.IsZero() {
		return nil, failure("record_corrupt")
	}
	switch r.Execution {
	case "", cp.ExecutionAttached, cp.ExecutionDetached:
	default:
		return nil, failure("record_corrupt")
	}
	switch r.State {
	case "accepted", "running", "reconciling", "failed", "cancelled":
	case "succeeded", "partial":
		if r.Snapshot == nil {
			return nil, failure("record_corrupt")
		}
	default:
		return nil, failure("record_corrupt")
	}
	if r.Snapshot != nil && (r.Snapshot.VaultID == "" || r.Snapshot.SnapshotID == "") {
		return nil, failure("record_corrupt")
	}
	return &r, nil
}

// Status never mutates or retries a run. Nonterminal durable records are
// conservatively projected as reconciling, including after a process crash.
func (s Store) Status(ctx context.Context, p StatusParams) (*Run, error) {
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
	return s.project(r), nil
}
func (s Store) Run(ctx context.Context, p Params) (*Run, error) {
	p, err := p.Normalize()
	if err != nil {
		return nil, err
	}
	if err = s.validate(ctx, p.AgentRef); err != nil {
		return nil, err
	}
	digest, err := cp.Digest(p)
	if err != nil {
		return nil, err
	}
	// A published record is immutable to retries. This check also lets a retry
	// observe a live operation without waiting on its process lock.
	existing, err := s.load(p.RunID)
	if err == nil {
		return s.repeated(existing, digest)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	unlock, err := s.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	existing, err = s.load(p.RunID)
	if err == nil {
		return s.repeated(existing, digest)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if s.Env == nil {
		return nil, failure("environment_unavailable")
	}
	launch := s.Launch
	if !p.Detach {
		launch = nil
	}
	execution := cp.ExecutionAttached
	if launch != nil {
		execution = cp.ExecutionDetached
	}
	var r *Run
	ps := planstore.Store{Home: s.Home, Env: s.Env, ValidateAgent: s.ValidateAgent}
	err = ps.WithAccepted(ctx, p.AgentRef, func(plan *cp.ServerPlan) error {
		if plan.Revision != p.ExpectedRevision {
			return failure("revision_conflict")
		}
		if p.Reason == "timer" && (plan.Schedule.Enabled == nil || !*plan.Schedule.Enabled) {
			return failure("schedule_disabled")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		now := time.Now().UTC()
		r = &Run{Schema: cp.BackupRunSchema, RunID: p.RunID, AgentRef: p.AgentRef, Request: p, RequestDigest: digest, Plan: *plan, State: "accepted", AcceptedAt: now, UpdatedAt: now, Components: []cp.BackupComponent{{Kind: "server_backup", State: "not_started"}}, Execution: execution, Stage: cp.StageQueued}
		if err := s.save(r); err != nil {
			return err
		}
		// The unit waits for the locks held here and claims the run only
		// while it is still accepted, so it can never replay it.
		if launch != nil && launch(ctx, p.RunID) == nil {
			return nil
		}
		release, err := s.runLock(p.RunID)
		if err != nil {
			// A unit whose launch reported failure did start and owns the
			// run; this connection must not execute it as well.
			return nil
		}
		defer release()
		if r.Execution != cp.ExecutionAttached {
			r.Execution = cp.ExecutionAttached
			if err := s.persist(r); err != nil {
				return err
			}
		}
		return s.execute(ctx, r)
	})
	if err != nil || r == nil {
		return r, err
	}
	return s.project(r), nil
}
func (s Store) repeated(r *Run, digest string) (*Run, error) {
	if r.RequestDigest != digest {
		return nil, failure("request_conflict")
	}
	return s.project(r), nil
}
