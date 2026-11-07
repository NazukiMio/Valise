package protection

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"valise/core/enroll"
	cp "valise/core/protection"
	"valise/porter/internal/seal"
)

// WithLegacyWrite serializes legacy changes against first adoption and updates.
func (s Store) WithLegacyWrite(instance string, run func() error) error {
	if enroll.ValidInstanceID(instance) != nil {
		return fail("instance_invalid", "实例名无效")
	}
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	if err = s.CheckLegacyWrite(instance); err != nil {
		return err
	}
	return run()
}

// WithLegacyTargetsWrite holds the same CAS lock across the legacy mutation.
func (s Store) WithLegacyTargetsWrite(run func() error) error {
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	if err = s.CheckLegacyTargetsWrite(); err != nil {
		return err
	}
	return run()
}

// CheckLegacyTargetsWrite rejects unversioned writes affecting shared references.
// The first N2 target API is deliberately conservative: changes must be prepared
// with all affected plan revisions, so the old writer cannot alter any target.
func (s Store) CheckLegacyTargetsWrite() error {
	if err := s.CheckPending(""); err != nil {
		return err
	}
	entries, err := os.ReadDir(filepath.Join(s.config(), "protection"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		return fail("revision_conflict", "服务器已有版本化保护计划，共享复制目标须核对所有引用版本后更改")
	}
	// Include first-adoption transactions before a plan file has been published.
	changes, err := os.ReadDir(s.state())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range changes {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		var r record
		if err := read(filepath.Join(s.state(), e.Name()), &r); err != nil {
			return err
		}
		if err := r.valid(); err != nil {
			return err
		}
		if r.State == "pending" || r.ReconcileRequired {
			return fail("result_unknown", "保护计划提交待核实，共享目标不可修改")
		}
	}
	return nil
}
func (s Store) validateImages(r record) error {
	if enroll.ValidInstanceID(r.AgentRef.InstanceID) != nil {
		return fail("record_corrupt", "事务实例名无效")
	}
	expected, err := s.snapshots(r.AgentRef.InstanceID)
	if err != nil {
		return err
	}
	if len(expected) != len(r.OldFiles) || len(expected) != len(r.NewFiles) {
		return fail("record_corrupt", "事务配置范围无效")
	}
	for path := range expected {
		if _, ok := r.OldFiles[path]; !ok {
			return fail("record_corrupt", "事务配置路径无效")
		}
		if _, ok := r.NewFiles[path]; !ok {
			return fail("record_corrupt", "事务配置路径无效")
		}
	}
	return nil
}

// Reconcile never reruns a new-plan install. verify only checks publication and
// real systemd state; rollback is only available before publication.
func (s Store) Reconcile(ctx context.Context, p cp.ReconcileParams) (*cp.Change, error) {
	if !cp.ValidID(p.ChangeID) || (p.Action != "verify" && p.Action != "rollback") {
		return nil, fail("request_invalid", "必须明确选择 verify 或 rollback")
	}
	if err := s.validate(ctx, p.AgentRef); err != nil {
		return nil, err
	}
	unlock, err := s.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	vu, err := (seal.Paths{Home: s.Home}).LockFor("protection")
	if err != nil {
		return nil, err
	}
	defer vu()
	var r record
	if err = read(filepath.Join(s.state(), p.ChangeID+".json"), &r); err != nil {
		return nil, err
	}
	if err = r.valid(); err != nil {
		return nil, err
	}
	if r.AgentRef != p.AgentRef {
		return nil, fail("identity_conflict", "事务不属于当前实例世代")
	}
	if err = s.validateImages(r); err != nil {
		return nil, err
	}
	status, err := s.statusRecord(r)
	if err != nil {
		return nil, err
	}
	current, err := s.load(r.AgentRef.InstanceID)
	if err != nil {
		return nil, err
	}
	if current != nil && current.Revision != r.OldRevision && current.Revision != r.NewRevision {
		return nil, fail("revision_conflict", "当前保护计划已不是此事务的版本")
	}
	if p.Action == "rollback" {
		if status.Committed || current != nil && current.Revision == r.NewRevision {
			return status, fail("revision_conflict", "新版本已发布，不能倒退版本")
		}
		if err = s.configure(ctx, r.AgentRef.InstanceID, r.OldFiles, r.OldActive, r.OldEnabled); err != nil {
			return status, err
		}
		if err = s.verifyImages(ctx, r); err != nil {
			return status, err
		}
		r.State = "failed"
		r.RollbackSucceeded = true
		r.ReconcileRequired = false
	} else if status.Committed {
		if current == nil {
			return status, fail("result_unknown", "已发布的计划记录缺失")
		}
		a, _ := cp.Digest(current)
		b, _ := cp.Digest(r.Plan)
		if a != b {
			return status, fail("record_corrupt", "发布计划与事务摘要不一致")
		}
		// Verify both unit bytes and actual manager state, without daemon-reload/start.
		files, err := s.snapshots(r.AgentRef.InstanceID)
		if err != nil {
			return status, err
		}
		a, _ = cp.Digest(files)
		b, _ = cp.Digest(r.NewFiles)
		if a != b {
			return status, fail("result_unknown", "已发布配置与事务不一致")
		}
		if err = s.verify(ctx, r.AgentRef.InstanceID, *r.Plan.Schedule.Enabled, r.NewFiles[s.periodPath(r.AgentRef.InstanceID)]); err != nil {
			return status, err
		}
		r.Committed = true
		r.State = "committed"
		r.ReconcileRequired = false
	} else {
		if err = s.verifyImages(ctx, r); err != nil {
			return status, err
		}
		r.State = "failed"
		r.RollbackSucceeded = true
		r.ReconcileRequired = false
	}
	if err = s.persist(&r); err != nil {
		return &r.Change, err
	}
	return &r.Change, nil
}
