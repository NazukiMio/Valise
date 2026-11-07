// Package protection implements the fixed protection-plan transaction. It never
// executes caller-supplied steps or commands.
package protection

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"valise/core/enroll"
	"valise/core/identity"
	cp "valise/core/protection"
	"valise/core/valise"
	porterenroll "valise/porter/internal/enroll"
)

type Store struct {
	Home          string
	Env           *porterenroll.Env
	ValidateAgent func(context.Context, identity.AgentRef) error
	// ValidateDeployment, when set, runs under the store lock before a new
	// transaction is recorded (see CheckDeployment). Nil skips the check,
	// for tests and explicitly run builds outside the formal install path.
	ValidateDeployment func(context.Context) error
	// Now is the server clock; nil means time.Now.
	Now func() time.Time
}

func (s Store) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
func (s Store) runsDir() string { return filepath.Join(s.Home, ".local", "state", "valise", "runs") }

func fail(code, reason string) error {
	return valise.New(code, reason, "重新读取计划；存在未完成事务时请显式核实恢复")
}
func (s Store) config() string { return filepath.Join(s.Home, ".config", "valise") }
func (s Store) state() string {
	return filepath.Join(s.Home, ".local", "state", "valise", "protection-changes")
}
func (s Store) planPath(id string) string { return filepath.Join(s.config(), "protection", id+".json") }
func (s Store) validate(ctx context.Context, a identity.AgentRef) error {
	if err := cp.ValidateAgent(a); err != nil {
		return err
	}
	if s.ValidateAgent == nil {
		return fail("identity_unbound", "没有可用的实例身份核实器")
	}
	return s.ValidateAgent(ctx, a)
}
func atomic(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return atomicBytes(path, append(b, '\n'), 0600)
}
func atomicBytes(path string, b []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".protection-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(b)
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
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
func read(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(b, v); err != nil {
		return fail("record_corrupt", "保护记录损坏，不能继续执行")
	}
	return nil
}
func (s Store) load(instance string) (*cp.ServerPlan, error) {
	if enroll.ValidInstanceID(instance) != nil {
		return nil, fail("instance_invalid", "实例名无效")
	}
	var p cp.ServerPlan
	if err := read(s.planPath(instance), &p); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if p.AgentRef.InstanceID != instance {
		return nil, fail("identity_conflict", "保护计划的实例身份不匹配")
	}
	return &p, nil
}
func (s Store) lock() (func(), error) {
	if err := os.MkdirAll(s.state(), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(s.state(), "store.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fail("resource_busy", "保护设置正在更新")
	}
	return func() { f.Close() }, nil
}

// CheckPending is read-only and fails closed on an unreadable transaction.
func (s Store) CheckPending(instance string) error {
	es, err := os.ReadDir(s.state())
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
		var r record
		if err := read(filepath.Join(s.state(), e.Name()), &r); err != nil {
			return err
		}
		if err := r.valid(); err != nil {
			return err
		}
		if r.AgentRef.InstanceID == instance && (r.State == "pending" || r.ReconcileRequired) {
			return fail("result_unknown", "保护计划事务尚待核实，已阻止按计划备份")
		}
	}
	return s.checkTargetChanges(instance)
}
func (s Store) CheckLegacyWrite(instance string) error {
	if err := s.CheckPending(instance); err != nil {
		return err
	}
	p, err := s.load(instance)
	if err != nil {
		return err
	}
	if p != nil {
		return fail("revision_conflict", "实例已采用版本化保护计划，旧设置入口不可修改")
	}
	return nil
}
func (s Store) Accepted(ctx context.Context, a identity.AgentRef) (*cp.ServerPlan, error) {
	if err := s.validate(ctx, a); err != nil {
		return nil, err
	}
	if err := s.CheckPending(a.InstanceID); err != nil {
		return nil, err
	}
	p, err := s.load(a.InstanceID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, fail("unsupported_capability", "实例尚未接受保护计划")
	}
	if p.AgentRef != a {
		return nil, fail("identity_conflict", "计划不属于当前实例世代")
	}
	return p, nil
}

// ReadAccepted reads a timer's bound identity and validates it against the server.
func (s Store) ReadAccepted(ctx context.Context, instance string) (*cp.ServerPlan, error) {
	p, err := s.load(instance)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, fail("unsupported_capability", "实例尚未接受保护计划")
	}
	return s.Accepted(ctx, p.AgentRef)
}
func (s Store) Get(ctx context.Context, p cp.GetParams) (*cp.View, error) {
	if err := s.validate(ctx, p.AgentRef); err != nil {
		return nil, err
	}
	plan, err := s.load(p.AgentRef.InstanceID)
	if err != nil {
		return nil, err
	}
	v := &cp.View{Mode: "legacy", Warnings: []string{}, MissedRuns: []cp.MissedRun{}, ServerNow: s.now()}
	if plan == nil {
		v.LegacySettings, err = s.legacy(p.AgentRef)
		if err != nil {
			return nil, err
		}
	}
	if plan != nil {
		if plan.AgentRef != p.AgentRef {
			return nil, fail("identity_conflict", "计划不属于当前实例世代")
		}
		v.Mode = "accepted"
		v.ServerPlan = plan
	}
	v.Timer = s.observe(ctx, p.AgentRef.InstanceID)
	if plan != nil {
		preview, err := s.calendar(ctx, plan.Desired)
		if err != nil {
			v.Warnings = append(v.Warnings, "目标 systemd 无法重新校验日历预览")
		} else {
			v.Timer.CalendarPreview = preview
		}
	}
	if err := s.CheckPending(p.AgentRef.InstanceID); err != nil {
		if ue, ok := valise.As(err); !ok || ue.Code != "result_unknown" {
			return nil, err
		}
		v.Warnings = append(v.Warnings, "保护设置有未完成事务，需要核实；按计划备份已阻止")
		v.PendingChanges = s.pendingChanges(p.AgentRef.InstanceID)
	}
	if plan != nil {
		s.missed(v, *plan)
	}
	if !v.Timer.Linger {
		v.Warnings = append(v.Warnings, "未确认 linger 已启用，退出登录后不能保证定时执行")
	}
	return v, nil
}

// missed only reads run records; problems become warnings of the view.
func (s Store) missed(v *cp.View, plan cp.ServerPlan) {
	runs, unreadable, err := cp.ReadTimerRuns(s.runsDir(), plan.AgentRef)
	if err != nil {
		v.Warnings = append(v.Warnings, "无法读取备份运行记录，错过的定时备份无法统计")
		return
	}
	if unreadable > 0 {
		v.Warnings = append(v.Warnings, "部分备份运行记录无法读取，错过的定时备份统计可能不完整")
	}
	if all, _, err := cp.ReadRuns(s.runsDir(), plan.AgentRef); err == nil {
		v.CopyStatus = cp.ComputeCopyStatus(plan, all)
	}
	missed, clockDoubt := cp.ComputeMissed(plan, runs, v.ServerNow)
	v.MissedRuns = missed
	if clockDoubt {
		v.Warnings = append(v.Warnings, "服务器时钟可能不准确：记录时间晚于服务器当前时间，错过统计按服务器时钟计算")
	}
}

// pendingChanges lists the instance's unsettled plan changes, read-only.
// Unreadable records were already reported by CheckPending.
func (s Store) pendingChanges(instance string) []cp.PendingChange {
	es, err := os.ReadDir(s.state())
	if err != nil {
		return nil
	}
	var out []cp.PendingChange
	for _, e := range es {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		var r record
		if read(filepath.Join(s.state(), e.Name()), &r) != nil || r.valid() != nil {
			continue
		}
		if r.AgentRef.InstanceID == instance && (r.State == "pending" || r.ReconcileRequired) {
			out = append(out, cp.PendingChange{ChangeID: r.ChangeID, State: r.State, Committed: r.Committed,
				OldRevision: r.OldRevision, NewRevision: r.NewRevision, ErrorCode: r.ErrorCode})
		}
	}
	return out
}
