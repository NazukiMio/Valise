package protection

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"valise/core/identity"
	cp "valise/core/protection"
	"valise/core/vaultrpc"
	"valise/porter/internal/seal"
)

// targetRecord is the durable intent of a versioned target change. It holds
// every byte the change writes, so a crash leaves a record that can be
// checked against the files without guessing.
type targetRecord struct {
	cp.TargetChange
	Target     vaultrpc.Target `json:"target"`
	OldTargets fileImage       `json:"old_targets"`
	NewTargets fileImage       `json:"new_targets"`
	OldPlans   []cp.ServerPlan `json:"old_plans"`
	NewPlans   []cp.ServerPlan `json:"new_plans"`
}

func (s Store) targetState() string {
	return filepath.Join(s.Home, ".local", "state", "valise", "target-changes")
}
func (s Store) targetsPath() string { return filepath.Join(s.config(), "targets.json") }

func (r targetRecord) valid() error {
	bad := fail("record_corrupt", "目标修改记录无效")
	if r.Schema != cp.TargetChangeSchema || !cp.ValidID(r.ChangeID) || r.RequestDigest == "" || r.TargetName == "" || r.Target.Name != r.TargetName || !r.NewTargets.Exists || len(r.Plans) == 0 || len(r.OldPlans) != len(r.Plans) || len(r.NewPlans) != len(r.Plans) {
		return bad
	}
	switch r.State {
	case "pending", "committed", "failed":
	default:
		return bad
	}
	for i, ref := range r.Plans {
		o, n := r.OldPlans[i], r.NewPlans[i]
		if o.Validate() != nil || n.Validate() != nil || o.AgentRef != ref.AgentRef || n.AgentRef != ref.AgentRef || o.Revision != ref.Revision || o.Revision == cp.Revision(math.MaxUint64) || n.Revision != o.Revision+1 {
			return bad
		}
		a, _ := cp.Digest(o.Desired)
		b, _ := cp.Digest(n.Desired)
		if a != b {
			return bad
		}
	}
	return nil
}

// checkTargetChanges fails closed on an unreadable record and refuses while a
// target change touching instance ("" = any) is unfinished.
func (s Store) checkTargetChanges(instance string) error {
	es, err := os.ReadDir(s.targetState())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range es {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		var r targetRecord
		if err := read(filepath.Join(s.targetState(), e.Name()), &r); err != nil {
			return err
		}
		if err := r.valid(); err != nil {
			return err
		}
		if r.State != "pending" && !r.ReconcileRequired {
			continue
		}
		for _, p := range r.Plans {
			if instance == "" || p.AgentRef.InstanceID == instance {
				return fail("result_unknown", "复制目标修改尚待核实，已阻止按计划备份与目标写入")
			}
		}
	}
	return nil
}

func readTargetList(im fileImage) ([]vaultrpc.Target, error) {
	if !im.Exists {
		return nil, nil
	}
	var tf struct {
		Targets []vaultrpc.Target `json:"targets"`
	}
	if json.Unmarshal(im.Data, &tf) != nil {
		return nil, fail("record_corrupt", "无法读取已有复制目标")
	}
	return tf.Targets, nil
}

// referencing lists, by instance name, the accepted plans copying to name.
func (s Store) referencing(name string) ([]cp.ServerPlan, error) {
	entries, err := os.ReadDir(filepath.Join(s.config(), "protection"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []cp.ServerPlan
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		plan, err := s.load(strings.TrimSuffix(e.Name(), ".json"))
		if err != nil {
			return nil, err
		}
		if plan == nil {
			continue
		}
		for _, c := range plan.ServerCopies {
			if c.TargetRef.ID == name {
				out = append(out, *plan)
				break
			}
		}
	}
	return out, nil
}

// dryRun applies seal.SetTarget, with all its validation, to a private copy
// of targets.json and returns the bytes it would write. Nothing outside the
// private scratch directory changes.
func (s Store) dryRun(old fileImage, t vaultrpc.Target) (fileImage, error) {
	if err := os.MkdirAll(s.targetState(), 0700); err != nil {
		return fileImage{}, err
	}
	scratch, err := os.MkdirTemp(s.targetState(), ".dryrun-")
	if err != nil {
		return fileImage{}, err
	}
	p := seal.Paths{Home: scratch}
	defer func() {
		// Remove only what this function created, file by file.
		_ = os.Remove(p.Targets())
		_ = os.Remove(filepath.Dir(p.Targets()))
		_ = os.Remove(filepath.Dir(filepath.Dir(p.Targets())))
		_ = os.Remove(scratch)
	}()
	if old.Exists {
		if err := atomicBytes(p.Targets(), old.Data, 0600); err != nil {
			return fileImage{}, err
		}
	}
	if err := seal.SetTarget(p, t); err != nil {
		return fileImage{}, err
	}
	return image(p.Targets())
}

type targetFacts struct {
	old     fileImage
	current vaultrpc.Target
	plans   []cp.ServerPlan
	refs    []cp.TargetPlanRef
}

func (s Store) targetFacts(name string) (*targetFacts, error) {
	if name == "" {
		return nil, fail("request_invalid", "缺少目标名称")
	}
	if err := s.CheckPending(""); err != nil {
		return nil, err
	}
	if err := s.checkUnfinished(); err != nil {
		return nil, err
	}
	old, err := image(s.targetsPath())
	if err != nil {
		return nil, err
	}
	list, err := readTargetList(old)
	if err != nil {
		return nil, err
	}
	f := &targetFacts{old: old}
	found := false
	for _, t := range list {
		if t.Name == name {
			f.current, found = t, true
			break
		}
	}
	if !found {
		return nil, fail("target_unknown", "服务器上没有名为 "+name+" 的复制目标；名称即标识，不支持改名或新增")
	}
	if f.plans, err = s.referencing(name); err != nil {
		return nil, err
	}
	if len(f.plans) == 0 {
		return nil, fail("target_unreferenced", "复制目标 "+name+" 未被保护计划引用，请使用原有目标设置入口")
	}
	for _, p := range f.plans {
		if p.Revision == cp.Revision(math.MaxUint64) {
			return nil, fail("revision_conflict", "保护计划版本已达到上限")
		}
		f.refs = append(f.refs, cp.TargetPlanRef{AgentRef: p.AgentRef, Revision: p.Revision})
	}
	return f, nil
}
func targetPreconditions(before vaultrpc.Target, refs []cp.TargetPlanRef) (string, error) {
	return cp.Digest(struct {
		Before vaultrpc.Target
		Plans  []cp.TargetPlanRef
	}{before, refs})
}

// PrepareTargetChange is read-only for the server configuration: it freezes
// the new definition of a plan-referenced target and the exact revisions of
// every plan copying to it.
func (s Store) PrepareTargetChange(ctx context.Context, p cp.TargetChangePrepareParams) (*cp.FrozenTargetChange, error) {
	f, err := s.targetFacts(p.Target.Name)
	if err != nil {
		return nil, err
	}
	a, _ := cp.Digest(f.current)
	b, _ := cp.Digest(p.Target)
	if a == b {
		return nil, fail("request_invalid", "目标定义没有变化")
	}
	if _, err = s.dryRun(f.old, p.Target); err != nil {
		return nil, err
	}
	digest, err := targetPreconditions(f.current, f.refs)
	if err != nil {
		return nil, err
	}
	id, err := identity.NewID()
	if err != nil {
		return nil, err
	}
	now := s.now()
	plan := &cp.FrozenTargetChange{PlanID: id, Kind: "target_change", CreatedAt: now, ExpiresAt: now.Add(10 * time.Minute), Before: f.current, Request: p, Plans: f.refs, PreconditionsDigest: digest}
	plan.PlanDigest, err = plan.Digest()
	return plan, err
}

func (s Store) persistTarget(r *targetRecord) error {
	r.UpdatedAt = s.now()
	return atomic(filepath.Join(s.targetState(), r.ChangeID+".json"), r)
}
func (s Store) settleTarget(r *targetRecord) *cp.TargetChange {
	if err := s.persistTarget(r); err != nil {
		out := r.TargetChange
		out.ReconcileRequired = true
		if out.ErrorCode == "" {
			out.ErrorCode = "record_write_failed"
		}
		return &out
	}
	return &r.TargetChange
}

// ApplyTargetChange writes targets.json and bumps every referencing plan only
// when all frozen revisions and the target definition still hold. The intent
// is recorded first; failures after that are returned in the TargetChange.
func (s Store) ApplyTargetChange(ctx context.Context, p cp.TargetChangeApplyParams) (*cp.TargetChange, error) {
	if !cp.ValidID(p.ChangeID) {
		return nil, fail("request_invalid", "事务标识无效")
	}
	digest, err := p.Plan.Digest()
	if err != nil || digest != p.Plan.PlanDigest || p.Plan.Kind != "target_change" || !cp.ValidID(p.Plan.PlanID) || len(p.Plan.Plans) == 0 || p.Plan.Before.Name != p.Plan.Request.Target.Name {
		return nil, fail("plan_stale", "冻结计划摘要无效")
	}
	requestDigest, err := cp.Digest(struct {
		Before  vaultrpc.Target
		Request cp.TargetChangePrepareParams
		Plans   []cp.TargetPlanRef
	}{p.Plan.Before, p.Plan.Request, p.Plan.Plans})
	if err != nil {
		return nil, err
	}
	unlock, err := s.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	vaultUnlock, err := (seal.Paths{Home: s.Home}).LockFor("protection")
	if err != nil {
		return nil, err
	}
	defer vaultUnlock()
	var existing targetRecord
	if err = read(filepath.Join(s.targetState(), p.ChangeID+".json"), &existing); err == nil {
		if err = existing.valid(); err != nil {
			return nil, err
		}
		if existing.RequestDigest != requestDigest {
			return nil, fail("request_conflict", "事务标识已经用于不同请求")
		}
		return s.resumeTarget(&existing)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	now := s.now()
	if now.After(p.Plan.ExpiresAt) || p.Plan.CreatedAt.After(now) || p.Plan.ExpiresAt.Sub(p.Plan.CreatedAt) != 10*time.Minute {
		return nil, fail("plan_expired", "首次执行有效期已结束")
	}
	if s.ValidateDeployment != nil {
		if err := s.ValidateDeployment(ctx); err != nil {
			return nil, err
		}
	}
	f, err := s.targetFacts(p.Plan.Request.Target.Name)
	if err != nil {
		return nil, err
	}
	fresh, err := targetPreconditions(f.current, f.refs)
	if err != nil {
		return nil, err
	}
	if fresh != p.Plan.PreconditionsDigest {
		return nil, fail("revision_conflict", "目标定义或引用它的保护计划版本已改变，请重新准备")
	}
	next, err := s.dryRun(f.old, p.Plan.Request.Target)
	if err != nil {
		return nil, err
	}
	r := targetRecord{TargetChange: cp.TargetChange{Schema: cp.TargetChangeSchema, ChangeID: p.ChangeID, TargetName: p.Plan.Request.Target.Name, RequestDigest: requestDigest, State: "pending", Plans: f.refs}, Target: p.Plan.Request.Target, OldTargets: f.old, NewTargets: next, OldPlans: f.plans}
	for _, old := range f.plans {
		n := old
		n.Revision = old.Revision + 1
		n.AcceptedAt = now
		r.NewPlans = append(r.NewPlans, n)
	}
	if err = s.persistTarget(&r); err != nil {
		// No durable intent: nothing has been changed.
		return nil, err
	}
	// SetTarget validates again and writes atomically; its output must be
	// exactly the dry-run bytes recorded in the intent.
	if err = seal.SetTarget(seal.Paths{Home: s.Home}, r.Target); err != nil {
		r.ErrorCode = errorCode(err)
		if cur, readErr := image(s.targetsPath()); readErr == nil && sameImage(cur, r.OldTargets) {
			r.State = "failed"
			r.RollbackSucceeded = true
		} else {
			r.ReconcileRequired = true
		}
		return s.settleTarget(&r), nil
	}
	return s.publishPlans(&r), nil
}
func sameImage(a, b fileImage) bool {
	x, _ := cp.Digest(a)
	y, _ := cp.Digest(b)
	return x == y
}
func samePlan(a *cp.ServerPlan, b cp.ServerPlan) bool {
	if a == nil {
		return false
	}
	x, _ := cp.Digest(a)
	y, _ := cp.Digest(b)
	return x == y
}

// publishPlans finishes a change whose targets.json holds the new bytes: it
// writes each plan still at its old revision, then records completion.
func (s Store) publishPlans(r *targetRecord) *cp.TargetChange {
	current, err := image(s.targetsPath())
	if err != nil || !sameImage(current, r.NewTargets) {
		r.ReconcileRequired = true
		r.ErrorCode = "result_unknown"
		return s.settleTarget(r)
	}
	for i, n := range r.NewPlans {
		cur, err := s.load(n.AgentRef.InstanceID)
		if err != nil {
			r.ReconcileRequired = true
			r.ErrorCode = errorCode(err)
			return s.settleTarget(r)
		}
		if samePlan(cur, n) {
			continue
		}
		if !samePlan(cur, r.OldPlans[i]) {
			r.ReconcileRequired = true
			r.ErrorCode = "result_unknown"
			return s.settleTarget(r)
		}
		if err := atomic(s.planPath(n.AgentRef.InstanceID), n); err != nil {
			r.ReconcileRequired = true
			r.ErrorCode = errorCode(err)
			return s.settleTarget(r)
		}
	}
	r.State = "committed"
	r.Committed = true
	r.ReconcileRequired = false
	r.ErrorCode = ""
	return s.settleTarget(r)
}

// resumeTarget is the explicit owner retry of the same change_id. A finished
// record is returned as is. An unfinished one is only compared with the
// files: untouched means nothing happened; new targets with each plan at its
// old or new revision is completed with the recorded plan bytes; anything
// else stays blocked for manual verification.
func (s Store) resumeTarget(r *targetRecord) (*cp.TargetChange, error) {
	if r.State != "pending" && !r.ReconcileRequired {
		return &r.TargetChange, nil
	}
	current, err := image(s.targetsPath())
	if err != nil {
		return nil, err
	}
	if sameImage(current, r.OldTargets) {
		for _, o := range r.OldPlans {
			cur, err := s.load(o.AgentRef.InstanceID)
			if err != nil {
				return nil, err
			}
			if !samePlan(cur, o) {
				r.ReconcileRequired = true
				r.ErrorCode = "result_unknown"
				return s.settleTarget(r), nil
			}
		}
		r.State = "failed"
		r.RollbackSucceeded = true
		r.ReconcileRequired = false
		return s.settleTarget(r), nil
	}
	return s.publishPlans(r), nil
}
