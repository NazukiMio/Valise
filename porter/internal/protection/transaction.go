package protection

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"

	"valise/core/identity"
	cp "valise/core/protection"
	"valise/core/valise"
	"valise/core/vault"
	"valise/core/vaultrpc"
	"valise/porter/internal/fault"
	"valise/porter/internal/seal"
)

const changeSchema = "valise-protection-change/1"

type fileImage struct {
	Exists bool   `json:"exists"`
	Data   []byte `json:"data,omitempty"`
}
type record struct {
	cp.Change
	Plan       cp.ServerPlan        `json:"plan"`
	OldFiles   map[string]fileImage `json:"old_files"`
	NewFiles   map[string]fileImage `json:"new_files"`
	OldActive  bool                 `json:"old_active"`
	OldEnabled bool                 `json:"old_enabled"`
}

func (r record) valid() error {
	if r.Schema != changeSchema || !cp.ValidID(r.ChangeID) || cp.ValidateAgent(r.AgentRef) != nil || r.Plan.Validate() != nil || r.Plan.AgentRef != r.AgentRef || r.NewRevision != r.Plan.Revision || r.OldRevision == cp.Revision(math.MaxUint64) || r.NewRevision != r.OldRevision+1 || r.RequestDigest == "" {
		return fail("record_corrupt", "保护事务记录无效")
	}
	switch r.State {
	case "pending", "committed", "failed":
	default:
		return fail("record_corrupt", "保护事务状态无效")
	}
	return nil
}
func (s Store) periodPath(instance string) string {
	return filepath.Join(s.config(), "instances", instance+".vault.json")
}
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
func image(path string) (fileImage, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return fileImage{}, nil
	}
	return fileImage{Exists: err == nil, Data: b}, err
}
func (s Store) snapshots(instance string) (map[string]fileImage, error) {
	svc, tmr := names(instance)
	dir := filepath.Join(s.Home, ".config", "systemd", "user")
	out := map[string]fileImage{}
	for _, path := range []string{filepath.Join(dir, svc), filepath.Join(dir, tmr), s.periodPath(instance)} {
		im, err := image(path)
		if err != nil {
			return nil, err
		}
		out[path] = im
	}
	return out, nil
}
func (s Store) preconditions(ctx context.Context, p cp.PrepareParams) (string, error) {
	if err := s.validate(ctx, p.AgentRef); err != nil {
		return "", err
	}
	if err := s.CheckPending(p.AgentRef.InstanceID); err != nil {
		return "", err
	}
	current, err := s.load(p.AgentRef.InstanceID)
	if err != nil {
		return "", err
	}
	revision := cp.Revision(0)
	if current != nil {
		// An earlier generation's plan is no longer in force (p.AgentRef is
		// the current one): the new plan replaces it from its revision on.
		revision = current.Revision
	}
	if revision != p.ExpectedRevision {
		return "", fail("revision_conflict", "保护计划版本已改变")
	}
	if revision == cp.Revision(math.MaxUint64) {
		return "", fail("revision_conflict", "保护计划版本已达到上限")
	}
	files, err := s.snapshots(p.AgentRef.InstanceID)
	if err != nil {
		return "", err
	}
	// Bind the accepted dossier, without hashing changing Agent file contents.
	dossier := map[string]fileImage{}
	for _, path := range []string{filepath.Join(s.config(), "instances", p.AgentRef.InstanceID+".json"), filepath.Join(s.config(), "instances", p.AgentRef.InstanceID+".profile.json"), filepath.Join(s.config(), "dossiers", p.AgentRef.InstallID+".json"), filepath.Join(s.config(), "dossiers", p.AgentRef.InstallID+".profile.json")} {
		im, err := image(path)
		if err != nil {
			return "", err
		}
		dossier[path] = im
	}
	targets, err := image(filepath.Join(s.config(), "targets.json"))
	if err != nil {
		return "", err
	}
	if err = s.validateTargets(p, targets); err != nil {
		return "", err
	}
	v := s.observe(ctx, p.AgentRef.InstanceID)
	if v.Status == "unknown" {
		return "", fail("timer_failed", "无法核实服务器当前定时器状态")
	}
	if v.ActiveState != "active" && v.ActiveState != "inactive" {
		return "", fail("resource_busy", "定时器尚未进入稳定状态")
	}
	if v.UnitFileState != "enabled" && v.UnitFileState != "disabled" {
		return "", fail("timer_failed", "定时器启用方式不受支持，需要核实")
	}
	return cp.Digest(struct {
		Current         *cp.ServerPlan
		Files           map[string]fileImage
		Dossier         map[string]fileImage
		Targets         fileImage
		Active, Enabled string
	}{current, files, dossier, targets, v.ActiveState, v.UnitFileState})
}
func (s Store) validateTargets(p cp.PrepareParams, im fileImage) error {
	if len(p.Desired.ServerCopies) == 0 {
		return nil
	}
	var tf struct {
		Targets []vaultrpc.Target `json:"targets"`
	}
	if !im.Exists || json.Unmarshal(im.Data, &tf) != nil {
		return fail("record_corrupt", "无法读取已有复制目标")
	}
	for _, c := range p.Desired.ServerCopies {
		if c.TargetRef.AuthorityID != p.AgentRef.ServerUID {
			return fail("identity_conflict", "复制目标不属于当前服务器")
		}
		found := false
		for _, t := range tf.Targets {
			if t.Name == c.TargetRef.ID {
				found = true
				break
			}
		}
		if !found {
			return fail("plan_stale", "引用的复制目标已不存在")
		}
	}
	return nil
}
func (s Store) Prepare(ctx context.Context, p cp.PrepareParams) (*cp.FrozenChange, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	digest, err := s.preconditions(ctx, p)
	if err != nil {
		return nil, err
	}
	preview, err := s.calendar(ctx, p.Desired)
	if err != nil {
		return nil, err
	}
	id, err := identity.NewID()
	if err != nil {
		return nil, err
	}
	before, err := s.before(p.AgentRef)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	plan := &cp.FrozenChange{Before: before, PlanID: id, Kind: "protection_change", CreatedAt: now, ExpiresAt: now.Add(10 * time.Minute), Request: p, PreconditionsDigest: digest, CalendarPreview: preview}
	plan.PlanDigest, err = plan.Digest()
	return plan, err
}
func (s Store) persist(r *record) error {
	r.UpdatedAt = time.Now().UTC()
	return atomic(filepath.Join(s.state(), r.ChangeID+".json"), r)
}
func (s Store) Apply(ctx context.Context, p cp.ApplyParams) (*cp.Change, error) {
	if !cp.ValidID(p.ChangeID) {
		return nil, fail("request_invalid", "事务标识无效")
	}
	if err := p.Plan.Request.Validate(); err != nil {
		return nil, err
	}
	digest, err := p.Plan.Digest()
	if err != nil || digest != p.Plan.PlanDigest || p.Plan.Kind != "protection_change" || !cp.ValidID(p.Plan.PlanID) {
		return nil, fail("plan_stale", "冻结计划摘要无效")
	}
	requestDigest, err := cp.Digest(p.Plan.Request)
	if err != nil {
		return nil, err
	}
	unlock, err := s.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	var existing record
	if err = read(filepath.Join(s.state(), p.ChangeID+".json"), &existing); err == nil {
		if err = existing.valid(); err != nil {
			return nil, err
		}
		if existing.RequestDigest != requestDigest || existing.AgentRef != p.Plan.Request.AgentRef {
			return nil, fail("request_conflict", "事务标识已经用于不同请求")
		}
		return s.statusRecord(existing)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	now := time.Now().UTC()
	if now.After(p.Plan.ExpiresAt) || p.Plan.CreatedAt.After(now) || p.Plan.ExpiresAt.Sub(p.Plan.CreatedAt) != 10*time.Minute {
		return nil, fail("plan_expired", "首次执行有效期已结束")
	}
	// Under the store lock a deploy cannot swap the installed porter, so a
	// passing check holds until the intent record below is written.
	if s.ValidateDeployment != nil {
		if err := s.ValidateDeployment(ctx); err != nil {
			return nil, err
		}
	}
	// Same order as WithAccepted: protection lock, then existing vault/B03 lock.
	vaultUnlock, err := (seal.Paths{Home: s.Home}).LockFor("protection")
	if err != nil {
		return nil, err
	}
	defer vaultUnlock()
	fresh, err := s.preconditions(ctx, p.Plan.Request)
	if err != nil {
		return nil, err
	}
	if fresh != p.Plan.PreconditionsDigest {
		return nil, fail("plan_stale", "计划依赖的服务器事实已改变")
	}
	before, err := s.before(p.Plan.Request.AgentRef)
	if err != nil {
		return nil, err
	}
	beforeDigest, _ := cp.Digest(before)
	reviewDigest, _ := cp.Digest(p.Plan.Before)
	if beforeDigest != reviewDigest {
		return nil, fail("plan_stale", "旧设置差异已改变，请重新查看计划")
	}
	if _, err = s.calendar(ctx, p.Plan.Request.Desired); err != nil {
		return nil, err
	}
	a := p.Plan.Request.AgentRef
	desired := p.Plan.Request.Desired
	old, err := s.snapshots(a.InstanceID)
	if err != nil {
		return nil, err
	}
	next, err := s.timerFiles(a.InstanceID, desired)
	if err != nil {
		return nil, err
	}
	pb, err := json.MarshalIndent(vault.PeriodSetting{Unit: desired.Period.Unit, TZ: desired.Period.Timezone}, "", "  ")
	if err != nil {
		return nil, err
	}
	next[s.periodPath(a.InstanceID)] = fileImage{Exists: true, Data: append(pb, '\n')}
	v := s.observe(ctx, a.InstanceID)
	r := record{Change: cp.Change{Schema: changeSchema, ChangeID: p.ChangeID, AgentRef: a, RequestDigest: requestDigest, State: "pending", OldRevision: p.Plan.Request.ExpectedRevision, NewRevision: p.Plan.Request.ExpectedRevision + 1}, Plan: cp.ServerPlan{SchemaVersion: cp.Schema, AgentRef: a, Revision: p.Plan.Request.ExpectedRevision + 1, Desired: desired, AcceptedAt: now}, OldFiles: old, NewFiles: next, OldActive: v.ActiveState == "active", OldEnabled: v.UnitFileState == "enabled"}
	if err = s.persist(&r); err != nil {
		// No durable intent: nothing has been changed.
		return nil, err
	}
	fault.Hit(s.Home, "protection-after-intent")
	// From here the change itself is the result. Failures are reported in the
	// returned Change (error_code), never as a bare RPC error that would hide a
	// completed rollback or a required reconciliation.
	if err = s.configure(ctx, a.InstanceID, next, *desired.Schedule.Enabled, *desired.Schedule.Enabled); err != nil {
		r.ErrorCode = errorCode(err)
		// A failed or cancelled attempt still restores the original timer at a safe boundary.
		restoreCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		defer cancel()
		restoreErr := s.configure(restoreCtx, a.InstanceID, old, r.OldActive, r.OldEnabled)
		if restoreErr == nil {
			restoreErr = s.verifyImages(restoreCtx, r)
		}
		if restoreErr == nil {
			r.State = "failed"
			r.RollbackSucceeded = true
		} else {
			r.ReconcileRequired = true
		}
		return s.settle(&r), nil
	}
	// Atomic publication is the point of no version rollback.
	if err = atomic(s.planPath(a.InstanceID), r.Plan); err != nil {
		r.ReconcileRequired = true
		r.ErrorCode = errorCode(err)
		return s.settle(&r), nil
	}
	fault.Hit(s.Home, "protection-after-publish")
	r.Committed = true
	r.State = "committed"
	r.ReconcileRequired = true
	if err = s.persist(&r); err != nil {
		r.ErrorCode = errorCode(err)
		return &r.Change, nil
	}
	actualFiles, readErr := s.snapshots(a.InstanceID)
	if readErr != nil {
		r.ErrorCode = errorCode(readErr)
		return s.settle(&r), nil
	}
	actualDigest, _ := cp.Digest(actualFiles)
	newDigest, _ := cp.Digest(next)
	if actualDigest != newDigest {
		r.ErrorCode = "result_unknown"
		return s.settle(&r), nil
	}
	if err = s.verify(ctx, a.InstanceID, *desired.Schedule.Enabled, next[s.periodPath(a.InstanceID)]); err != nil {
		r.ErrorCode = errorCode(err)
		return s.settle(&r), nil
	}
	r.ReconcileRequired = false
	return s.settle(&r), nil
}

// errorCode keeps only the stable classification of a failure.
func errorCode(err error) string {
	if ue, ok := valise.As(err); ok && ue.Code != "" {
		return ue.Code
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "cancelled"
	}
	return "internal"
}

// settle persists the outcome. If even that fails, the durable record is still
// the earlier one, so the caller is told to reconcile instead of trusting it.
func (s Store) settle(r *record) *cp.Change {
	if err := s.persist(r); err != nil {
		out := r.Change
		out.ReconcileRequired = true
		if out.ErrorCode == "" {
			out.ErrorCode = "record_write_failed"
		}
		return &out
	}
	return &r.Change
}
func (s Store) verifyImages(ctx context.Context, r record) error {
	files, err := s.snapshots(r.AgentRef.InstanceID)
	if err != nil {
		return err
	}
	a, _ := cp.Digest(files)
	b, _ := cp.Digest(r.OldFiles)
	if a != b {
		return fail("result_unknown", "旧配置尚未恢复一致")
	}
	v := s.observe(ctx, r.AgentRef.InstanceID)
	if v.Status == "unknown" || (v.ActiveState == "active") != r.OldActive || (v.UnitFileState == "enabled") != r.OldEnabled {
		return fail("result_unknown", "旧定时器状态尚未核实恢复")
	}
	return nil
}

// statusRecord observes publication after the crash window; it never replays a step.
func (s Store) statusRecord(r record) (*cp.Change, error) {
	if r.State == "pending" {
		r.ReconcileRequired = true
	}
	p, err := s.load(r.AgentRef.InstanceID)
	if err != nil {
		return nil, err
	}
	if p != nil && p.Revision == r.NewRevision {
		a, _ := cp.Digest(p)
		b, _ := cp.Digest(r.Plan)
		if a == b {
			r.Committed = true
			r.State = "committed"
		}
	}
	if r.State == "pending" {
		r.ReconcileRequired = true
	}
	return &r.Change, nil
}
func (s Store) Status(ctx context.Context, p cp.StatusParams) (*cp.Change, error) {
	if !cp.ValidID(p.ChangeID) {
		return nil, fail("request_invalid", "事务标识无效")
	}
	if err := s.validate(ctx, p.AgentRef); err != nil {
		return nil, err
	}
	var r record
	if err := read(filepath.Join(s.state(), p.ChangeID+".json"), &r); err != nil {
		return nil, err
	}
	if err := r.valid(); err != nil {
		return nil, err
	}
	if r.AgentRef != p.AgentRef {
		return nil, fail("identity_conflict", "事务不属于当前实例世代")
	}
	return s.statusRecord(r)
}

// WithAccepted freezes the plan and period projection until the fixed backup returns.
// The callback must not reenter this store or acquire the protection lock again.
func (s Store) WithAccepted(ctx context.Context, a identity.AgentRef, run func(*cp.ServerPlan) error) error {
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	p, err := s.Accepted(ctx, a)
	if err != nil {
		return err
	}
	im, err := image(s.periodPath(a.InstanceID))
	if err != nil {
		return err
	}
	var setting vault.PeriodSetting
	if !im.Exists || json.Unmarshal(im.Data, &setting) != nil || setting.Unit != p.Period.Unit || setting.TZ != p.Period.Timezone {
		return fail("result_unknown", "周期兼容投影与已接受计划不一致")
	}
	return run(p)
}
