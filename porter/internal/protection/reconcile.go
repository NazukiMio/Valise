package protection

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"valise/core/enroll"
	cp "valise/core/protection"
	"valise/core/valise"
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

// WithLegacyTargetsWrite holds the same CAS lock across the legacy mutation
// of the named target.
func (s Store) WithLegacyTargetsWrite(name string, run func() error) error {
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	if err = s.CheckLegacyTargetsWrite(name); err != nil {
		return err
	}
	return run()
}

// WithTargetRemove holds the same lock while a copy location is forgotten.
// Locations referenced by any plan in force stay: forgetting them would
// silently change those plans. A plan of an earlier generation (the agent
// was enrolled again) is not in force and does not keep its locations. While
// any plan transaction is unfinished nothing is removed. All blocking active
// instance IDs are collected and returned together in target_in_use.
func (s Store) WithTargetRemove(ctx context.Context, name string, run func() error) error {
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	if err := s.CheckPending(""); err != nil {
		return err
	}
	if err := s.checkUnfinished(); err != nil {
		return err
	}
	entries, err := os.ReadDir(filepath.Join(s.config(), "protection"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var blockers []string
	seen := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		plan, err := s.load(strings.TrimSuffix(e.Name(), ".json"))
		if err != nil {
			return err
		}
		if plan == nil {
			continue
		}
		referenced := false
		for _, c := range plan.ServerCopies {
			if c.TargetRef.ID == name {
				referenced = true
				break
			}
		}
		if !referenced {
			continue
		}
		// Only a plan known to be an earlier generation's lets go.
		verr := s.validate(ctx, plan.AgentRef)
		if ue, ok := valise.As(verr); ok && ue.Code == "identity_conflict" {
			continue
		}
		if verr != nil {
			return verr
		}
		if !seen[plan.AgentRef.InstanceID] {
			seen[plan.AgentRef.InstanceID] = true
			blockers = append(blockers, plan.AgentRef.InstanceID)
		}
	}
	if len(blockers) > 0 {
		sort.Strings(blockers)
		detail, _ := json.Marshal(struct {
			Target            string   `json:"target"`
			BlockingInstances []string `json:"blocking_instances"`
		}{Target: name, BlockingInstances: blockers})
		suggestion := "先在这个 Agent 的保护计划里取消这个位置，再移除"
		if len(blockers) > 1 {
			suggestion = "先在这些 Agent 的保护计划里取消这个位置，再移除"
		}
		return valise.New("target_in_use",
			"备份位置 "+name+" 正被 "+strings.Join(blockers, "、")+" 的保护计划使用，没有移除",
			suggestion).WithDetail(string(detail))
	}
	return run()
}

// CheckLegacyTargetsWrite rejects an unversioned write to a target that an
// accepted plan copies to: changing it would silently change that plan, so it
// must go through a versioned change of every referencing plan. Targets no
// plan references keep their legacy behaviour (their auto setting only drives
// legacy instances; planned backups ignore targets.auto). While any plan
// transaction is unfinished, every target write is refused, because the
// pending plan's copies are not yet known to be settled.
func (s Store) CheckLegacyTargetsWrite(name string) error {
	if err := s.CheckPending(""); err != nil {
		return err
	}
	if err := s.checkUnfinished(); err != nil {
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
		plan, err := s.load(strings.TrimSuffix(e.Name(), ".json"))
		if err != nil {
			return err
		}
		if plan == nil {
			continue
		}
		for _, c := range plan.ServerCopies {
			if c.TargetRef.ID == name {
				return fail("revision_conflict", "复制目标 "+name+" 已被实例 "+plan.AgentRef.InstanceID+" 的保护计划引用，须通过版本化计划更改")
			}
		}
	}
	return nil
}

// checkUnfinished refuses while any protection-plan transaction of any
// instance, including a first adoption before its plan file exists, or any
// versioned target change is unfinished.
func (s Store) checkUnfinished() error {
	changes, err := os.ReadDir(s.state())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
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
			return fail("result_unknown", "保护计划提交待核实，复制目标暂不可修改")
		}
	}
	return s.checkTargetChanges("")
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
