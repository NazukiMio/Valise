package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"

	"valise/core/environment"
	"valise/core/identity"
	"valise/core/journey"
	"valise/core/protection"
	"valise/core/revive"
	"valise/core/valise"
)

// RestoreTask is an index, never an execution permission. It contains no
// profile, decrypted header, file contents, command output or human approval.
type RestoreTask struct {
	Kind              string                   `json:"kind,omitempty"`
	EnvironmentMode   environment.Mode         `json:"environment_mode,omitempty"`
	SnapshotTime      time.Time                `json:"snapshot_time,omitempty"`
	SnapshotTier      string                   `json:"snapshot_tier,omitempty"`
	RuntimeRequested  bool                     `json:"runtime_requested,omitempty"`
	OldCleanupState   string                   `json:"old_cleanup_state,omitempty"`
	TargetAgent       *identity.AgentRef       `json:"target_agent,omitempty"`
	PreBackupSnapshot string                   `json:"pre_backup_snapshot,omitempty"`
	Rollback          *revive.RollbackEvidence `json:"rollback,omitempty"`
	CancelRequested   bool                     `json:"cancel_requested,omitempty"`
	TrustDigest       string                   `json:"target_trust_digest,omitempty"`
	OpID              string                   `json:"op_id"`
	Target            string                   `json:"target"`
	From              string                   `json:"from"`
	Mode              string                   `json:"mode"`
	Snapshot          journey.SnapshotRef      `json:"snapshot"`
	Instance          string                   `json:"instance"`
	State             string                   `json:"state"`
	Stage             string                   `json:"stage"`
	Created           time.Time                `json:"created"`
	Updated           time.Time                `json:"updated"`
	Verified          bool                     `json:"stage_verified,omitempty"`
	SwapState         string                   `json:"swap_state,omitempty"`
	Relation          bool                     `json:"relation_recorded,omitempty"`
	Prev              []string                 `json:"prev,omitempty"`
}

func (s *Service) restoreTaskPath(op string) string {
	return filepath.Join(s.Dirs.Data, "restore-tasks", op+".json")
}
func (s *Service) saveRestore(t RestoreTask) error {
	if err := os.MkdirAll(filepath.Dir(s.restoreTaskPath(t.OpID)), 0700); err != nil {
		return err
	}
	t.Updated = time.Now().UTC()
	return protectionAtomic(s.restoreTaskPath(t.OpID), t)
}

func (s *Service) ListRestoreTasks() ([]RestoreTask, error) {
	var out []RestoreTask
	entries, err := os.ReadDir(filepath.Join(s.Dirs.Data, "restore-tasks"))
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		var t RestoreTask
		b, err := os.ReadFile(filepath.Join(s.Dirs.Data, "restore-tasks", e.Name()))
		if err != nil || json.Unmarshal(b, &t) != nil || !validRestoreTaskID(t) || e.Name() != t.OpID+".json" || t.Kind != "" && t.Kind != RestoreKindEnvironment {
			return nil, restoreError("record_corrupt", "恢复任务索引无法读取")
		}
		if t.State == "running" || t.State == "waiting" {
			t.State = "reconciling"
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out, nil
}

func validRestoreTaskID(t RestoreTask) bool {
	if t.Kind == RestoreKindEnvironment {
		return protection.ValidID(t.OpID)
	}
	return revive.ValidOpID(t.OpID)
}

// RunRestore holds its local execution lock through both confirmations. The
// caller runs it off the UI thread. Reusing a prepared operation never replays.
func (s *Service) RunRestore(ctx context.Context, p *PreparedRestore, human ReviveConfirm, changed func(RestoreTask)) (RestoreTask, error) {
	rp := p.Revive
	t := RestoreTask{OpID: rp.Plan.OpID, Target: rp.Target, From: rp.From, Mode: rp.Plan.Mode, Snapshot: p.Request.Snapshot, Instance: rp.Plan.InstanceID, State: "running", Stage: "inspect", Created: time.Now().UTC()}
	if err := os.MkdirAll(filepath.Dir(s.restoreTaskPath(t.OpID)), 0700); err != nil {
		return t, err
	}
	trust, err := s.restoreTrust(t.Target)
	if err != nil {
		return t, err
	}
	t.TrustDigest = trust
	unlock, err := protectionStoreLock(s.restoreTaskPath(t.OpID) + ".lock")
	if err != nil {
		return t, err
	}
	defer unlock()
	if _, err = os.Stat(s.restoreTaskPath(t.OpID)); !os.IsNotExist(err) {
		return t, restoreError("request_conflict", "这项恢复已有任务记录，请核实原操作")
	}
	if err = s.validateRestore(ctx, p, ""); err != nil {
		t.State = "failed"
		t.Stage = ""
		return t, err
	}
	if err = s.saveRestore(t); err != nil {
		return t, err
	}
	publish := func() {
		if changed != nil {
			changed(t)
		}
	}
	publish()
	var recordErr error
	store := func() {
		if rp.PreRestoreBackup != nil {
			t.PreBackupSnapshot = rp.PreRestoreBackup.SnapshotID
		}
		if recordErr == nil {
			recordErr = s.saveRestore(t)
		}
		publish()
	}
	parentProgress, _ := ctx.Value(progressKey{}).(func(Progress))
	ctx = WithProgress(ctx, func(pr Progress) {
		changedStage := t.Stage != pr.Stage
		t.Stage = pr.Stage
		if parentProgress != nil {
			parentProgress(pr)
		}
		if changedStage {
			store()
		}
	})
	var checkErr error
	res, runErr := s.ExecuteReviveConfirmed(ctx, rp, "", ReviveConfirm{
		BackupCurrent:    human.BackupCurrent,
		UnverifiedSource: human.UnverifiedSource,
		Swap: func(ver *revive.StageVerifyResult) bool {
			t.Verified = true
			t.State = "waiting"
			t.Stage = "confirm"
			store()
			if recordErr != nil {
				return false
			}
			if checkErr = s.validateRestore(ctx, p, t.OpID); checkErr != nil {
				return false
			}
			p.mu.Lock()
			p.stageExpires = time.Now().Add(10 * time.Minute)
			p.mu.Unlock()
			if human.Swap == nil || !human.Swap(ver) {
				return false
			}
			p.mu.Lock()
			expired := time.Now().After(p.stageExpires)
			p.mu.Unlock()
			if expired {
				checkErr = restoreError("confirmation_expired", "交换确认已过期，活动目录未替换")
				return false
			}
			if checkErr = s.validateRestore(ctx, p, t.OpID); checkErr != nil {
				return false
			}
			t.State = "running"
			t.Stage = "swap"
			store()
			return recordErr == nil
		},
	})
	if checkErr != nil {
		runErr = checkErr
	}
	if rp.PreRestoreBackup != nil {
		t.PreBackupSnapshot = rp.PreRestoreBackup.SnapshotID
	}
	if ue, ok := valise.As(runErr); ctx.Err() == context.Canceled || ok && ue.Code == "cancelled" {
		t.CancelRequested = true
	}
	t.State = "reconciling"
	t.Stage = ""
	if res != nil {
		t.SwapState = res.State
		t.Rollback = res.Rollback
		t.Prev = res.Prev
	}
	// Only the remote journal, including final identity commit, settles state.
	qctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if observed, _, e := s.InspectRestore(qctx, t); e == nil {
		t = observed
	} else if res == nil && runErr == nil {
		runErr = e
	}
	if recordErr != nil {
		return t, recordErr
	}
	if err = s.saveRestore(t); err != nil {
		return t, err
	}
	publish()
	return t, runErr
}
