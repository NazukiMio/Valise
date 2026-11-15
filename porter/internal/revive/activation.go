package revive

import (
	"context"
	"errors"
	"os"
	"time"

	"valise/core/environment"
	"valise/core/identity"
	"valise/core/revive"
	"valise/core/valise"
	"valise/porter/internal/environments"
	"valise/porter/internal/isolated"
	"valise/porter/internal/managedruntime"
	"valise/porter/internal/seal"
)

// MaterialVerifier is an internal source/binding adapter. It must verify the
// persisted private program manifest for this exact incarnation and digest;
// absence is an error, never a fallback to trusting unit text or a JSON flag.
type MaterialVerifier func(context.Context, identity.AgentRef, environment.Ref, string) error

type ActivationRunner struct {
	runner  *Runner
	runtime managedruntime.Controller
	verify  MaterialVerifier
}

func NewActivationRunner(r *Runner, verify MaterialVerifier) *ActivationRunner {
	return &ActivationRunner{runner: r, runtime: managedruntime.Controller{Home: r.Store.Home}, verify: verify}
}

// NewBoundActivationRunner reconstructs the concrete material verifier from
// private records; no in-memory test closure is needed after reconnect.
func NewBoundActivationRunner(r *Runner) *ActivationRunner {
	return NewActivationRunner(r, (environments.ProgramStore{Home: r.Store.Home}).Verify)
}

type ActivationReview struct {
	Record    *ActivationRecord                  `json:"record"`
	Phase     environment.Phase                  `json:"phase,omitempty"`
	Current   *managedruntime.Observation        `json:"current,omitempty"`
	Gateway   *managedruntime.GatewayObservation `json:"gateway,omitempty"`
	IssueCode string                             `json:"issue_code,omitempty"`
	Native    *managedruntime.Observation        `json:"native,omitempty"`
}

func activationError(code string) error {
	reason, next := "并行恢复的一个步骤未完成", "查看原恢复记录中的失败步骤，再选择核实或切回"
	switch code {
	case "activation_stale":
		reason, next = "这个恢复记录已不再对应当前环境，或准备内容已改变", "刷新当前 Agent 的恢复记录后再操作"
	case "activation_not_prepared":
		reason, next = "新环境还没有完成准备和校验", "完成原操作的准备后再切换，当前 Agent 可以继续使用"
	case "activation_materials_unavailable":
		reason, next = "当前适配尚不能核对这套独立程序", "保留当前环境；等待此版本的恢复适配后再切换"
	case "activation_materials_changed", "activation_original_materials_changed":
		reason, next = "准备好的程序文件已变化，无法按原计划启动", "核对原恢复记录的程序准备结果；不要借用当前安装替代原版本"
	case "activation_original_unknown":
		reason, next = "无法核实当前运行环境及其启动方式", "重新检查当前 Agent 的运行状态，再准备恢复"
	case "activation_stage_changed":
		reason, next = "准备好的数据已变化或无法完整核实，没有切换原环境", "查询原准备记录；核实后取消这次准备，再重新选择备份"
	case "activation_native_source_changed":
		reason, next = "原程序在准备后发生变化，尚未停止原 Agent", "保留当前环境；核实程序更新后重新准备，不沿用旧回退程序"
	case "activation_native_unknown", "activation_native_not_stopped":
		reason, next = "原生启动入口的运行或停止状态尚未核实，不能启动另一环境", "保留当前目录，检查原恢复记录中的原生服务状态后再明确核实或切回"
	case "activation_readiness_unsupported":
		reason, next = "这个版本尚无可用于正式切换的就绪检查", "保留原环境；可以先校验备份文件和已支持的隔离重建"
	case "activation_stop_candidate_failed", "activation_disable_return_failed":
		reason, next = "尚未核实候选环境已停止并关闭开机启动", "先在原恢复记录中核实候选，确认停止前不能启动旧环境"
	case "activation_gateway_candidate_failed":
		reason, next = "新环境的网关未通过检查", "查看本次操作的切回结果，失败候选的数据会保留"
	case "activation_gateway_original_failed":
		reason, next = "原数据已退回，但原环境的网关仍未通过检查", "检查原环境的启动与网关结果，再处理本次记录"
	}
	return valise.New(code, reason, next)
}

func (a *ActivationRunner) home() string { return a.runner.Store.Home }
func (a *ActivationRunner) archive() managedruntime.Controller {
	return managedruntime.Controller{Home: a.home()}
}

func (a *ActivationRunner) registry(rec *ActivationRecord) (environment.Registry, error) {
	r, err := (environments.Store{Home: a.home()}).Load(rec.Plan.Agent)
	if err != nil {
		return r, err
	}
	o := r.Operation
	if o == nil || o.ID != rec.Plan.OperationID || o.Snapshot != rec.Plan.Snapshot || o.Original != rec.Plan.Original || o.Candidate != rec.Plan.Candidate {
		return r, activationError("activation_stale")
	}
	j, err := a.runner.Store.Load(rec.Plan.DataOpID)
	if err != nil {
		return r, err
	}
	if err := checkManagedData(a.runner.Store, j, o.Phase); err != nil {
		return r, err
	}
	if j.ManagedData.OperationID != o.ID || j.ManagedData.Snapshot != o.Snapshot || j.ManagedData.Agent != r.Agent {
		return r, activationError("activation_stale")
	}
	if rec.BindingDigest != "" && (j.ManagedData.PlanDigest != rec.DataPlanDigest || j.ManagedData.ManifestDigest != rec.DataManifestDigest) {
		return r, activationError("activation_stale")
	}
	return r, nil
}

func (a *ActivationRunner) transition(rec *ActivationRecord, fn func(environment.Registry, string) (environment.Registry, error)) error {
	r, err := a.registry(rec)
	if err != nil {
		return err
	}
	_, err = (environments.Store{Home: a.home()}).Update(r.Agent, r.Revision, func(r environment.Registry) (environment.Registry, error) { return fn(r, rec.Plan.OperationID) })
	return err
}

// Prepare archives only compiler-produced definitions and freezes a completed
// preparation. It neither stops A nor trusts a caller's readiness boolean.
func (a *ActivationRunner) Prepare(ctx context.Context, plan ActivationPlan, original, candidate managedruntime.Definition) (*ActivationReview, error) {
	if ctx == nil || a.verify == nil {
		return nil, activationError("activation_materials_unavailable")
	}
	unlock, err := (seal.Paths{Home: a.home()}).LockFor("environment_prepare")
	if err != nil {
		return nil, err
	}
	defer unlock()
	return a.prepareLocked(ctx, plan, original, candidate)
}

// prepareLocked is shared with the trusted preparation controller, which
// already owns the same cross-process recovery lock.
func (a *ActivationRunner) prepareLocked(ctx context.Context, plan ActivationPlan, original, candidate managedruntime.Definition) (*ActivationReview, error) {
	if old, err := loadActivation(a.home(), plan.Agent, plan.OperationID); err == nil {
		if old.Plan.DataOpID != plan.DataOpID || old.OriginalDefinition != original.Digest() || old.CandidateDefinition != candidate.Digest() || environments.Digest(old.Plan) != environments.Digest(plan) {
			return nil, activationError("activation_stale")
		}
		return a.Query(ctx, plan.Agent, plan.OperationID)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	rec := &ActivationRecord{Schema: activationSchema, Plan: plan, OriginalDefinition: original.Digest(), CandidateDefinition: candidate.Digest(), State: "ready", Facts: map[string]ActivationFact{}}
	r, err := a.registry(rec)
	if err != nil || r.Operation.Phase != environment.Prepared {
		return nil, activationError("activation_not_prepared")
	}
	j, err := a.runner.Store.Load(plan.DataOpID)
	if err != nil {
		return nil, err
	}
	if j.State != revive.StatePlanned || !j.StageVerified || !j.ManifestSaved || !managedManifestMatches(j, j.Manifest) || original.Name() != candidate.Name() ||
		original.Name() != "valise-agent-"+environment.Key(plan.Agent)+".service" || environments.Digest(plan.CandidateProfile) != environments.Digest(j.Plan.Profile) || environments.Digest(plan.OriginalProfile) != environments.Digest(j.Plan.Profile) {
		return nil, activationError("activation_not_prepared")
	}
	rec.DataPlanDigest, rec.DataManifestDigest = j.ManagedData.PlanDigest, j.ManagedData.ManifestDigest
	for _, p := range []struct {
		ref    environment.Ref
		digest string
	}{{plan.Original, plan.OriginalMaterials}, {plan.Candidate, plan.CandidateMaterials}} {
		if len(p.digest) != 64 || a.verify(ctx, plan.Agent, p.ref, p.digest) != nil {
			return nil, activationError("activation_materials_changed")
		}
	}
	check, err := isolated.CheckFor(plan.CandidateProfile)
	if err != nil || check.Offline != nil {
		return nil, activationError("activation_readiness_unsupported")
	}
	oldCheck, err := isolated.CheckFor(plan.OriginalProfile)
	if err != nil || oldCheck.Offline != nil {
		return nil, activationError("activation_readiness_unsupported")
	}
	if err := a.verifyNativeSource(ctx, rec); err != nil {
		return nil, activationError("activation_native_source_changed")
	}
	ob, err := a.readOriginal(ctx, rec, original)
	if err != nil || !ob.Running || ob.PendingJob || ob.UnitFileState != "enabled" && ob.UnitFileState != "disabled" {
		return nil, activationError("activation_original_unknown")
	}
	if rec.Plan.NativeOriginal != nil {
		if err := a.runtime.RequireAbsent(ctx, original); err != nil {
			return nil, activationError("activation_original_unknown")
		}
	}
	rec.EnableOnBoot = ob.UnitFileState == "enabled"
	rec.BindingDigest = activationDigest(rec)
	if err := a.archive().Remember(original); err != nil {
		return nil, err
	}
	if err := a.archive().Remember(candidate); err != nil {
		return nil, err
	}
	if err := saveActivation(a.home(), rec); err != nil {
		return nil, err
	}
	return a.Query(ctx, plan.Agent, plan.OperationID)
}

// Query never initializes a record or replays a step, even when a former
// process died after saving a running intent.
func (a *ActivationRunner) Query(ctx context.Context, agent identity.AgentRef, op string) (*ActivationReview, error) {
	rec, err := loadActivation(a.home(), agent, op)
	if err != nil {
		return nil, err
	}
	review := &ActivationReview{Record: rec, IssueCode: rec.IssueCode}
	r, err := a.registry(rec)
	if err != nil {
		review.IssueCode = "activation_stale"
		return review, nil
	}
	review.Phase = r.Operation.Phase
	if ref := rec.Plan.NativeOriginal; ref != nil {
		native, e := a.runtime.ReadNative(ctx, *ref)
		if e != nil {
			review.IssueCode = "activation_native_unknown"
			return review, nil
		}
		review.Native = &native
	}
	d, ob, err := a.installed(ctx, rec)
	if err != nil {
		if review.Native != nil && nativeMatches(rec, *review.Native) {
			original, e := a.archive().Remembered(agent, rec.OriginalDefinition)
			if e == nil && a.runtime.RequireAbsent(ctx, original) == nil {
				review.Current = review.Native
				gateway, e := a.runtime.CheckNativeHTTP(ctx, *rec.Plan.NativeOriginal, rec.Plan.OriginalProfile)
				review.Gateway = &gateway
				if e != nil {
					review.IssueCode = "activation_gateway_unverified"
				}
				return a.finishQuery(review, r)
			}
		}
		review.IssueCode = "activation_runtime_unknown"
		return a.finishQuery(review, r)
	}
	review.Current = &ob
	if review.Native != nil && (!review.Native.Stopped || review.Native.UnitFileState != "disabled") {
		review.IssueCode = "activation_native_not_stopped"
		return a.finishQuery(review, r)
	}
	if ob.Running {
		p := rec.Plan.OriginalProfile
		if d.Digest() == rec.CandidateDefinition {
			p = rec.Plan.CandidateProfile
		}
		gateway, err := a.runtime.CheckHTTP(ctx, d, p)
		review.Gateway = &gateway
		if err != nil {
			review.IssueCode = "activation_gateway_unverified"
		}
	}
	return a.finishQuery(review, r)
}

func (a *ActivationRunner) finishQuery(review *ActivationReview, r environment.Registry) (*ActivationReview, error) {
	final, err := a.registry(review.Record)
	if err != nil || final.Revision != r.Revision {
		review.Current, review.Gateway, review.Native, review.IssueCode = nil, nil, nil, "activation_changed"
	}
	return review, nil
}

func nativeMatches(rec *ActivationRecord, ob managedruntime.Observation) bool {
	r := rec.Plan.NativeOriginal
	return r != nil && ob.Running && !ob.PendingJob && ob.MainPID == r.MainPID && ob.KernelStart == r.KernelStart && ob.ControlGroup == r.ControlGroup
}

func (a *ActivationRunner) readOriginal(ctx context.Context, rec *ActivationRecord, d managedruntime.Definition) (managedruntime.Observation, error) {
	if ref := rec.Plan.NativeOriginal; ref != nil {
		if ref.Validate(rec.Plan.OriginalProfile) != nil {
			return managedruntime.Observation{}, managedruntime.ErrDefinition
		}
		ob, err := a.runtime.ReadNative(ctx, *ref)
		if err != nil || !nativeMatches(rec, ob) || (ob.UnitFileState == "enabled") != ref.EnableOnBoot {
			return ob, managedruntime.ErrState
		}
		return ob, nil
	}
	return a.runtime.Read(ctx, d)
}

func (a *ActivationRunner) installed(ctx context.Context, rec *ActivationRecord) (managedruntime.Definition, managedruntime.Observation, error) {
	for _, expected := range []string{rec.CandidateDefinition, rec.OriginalDefinition} {
		d, err := a.runtime.Load(rec.Plan.Agent, expected)
		if err != nil {
			continue
		}
		ob, err := a.runtime.Read(ctx, d)
		return d, ob, err
	}
	return managedruntime.Definition{}, managedruntime.Observation{}, managedruntime.ErrDefinition
}

func (a *ActivationRunner) mark(rec *ActivationRecord, state string) error {
	rec.State = state
	if state == "awaiting_human" || state == "rolled_back" {
		rec.IssueCode = ""
	}
	return saveActivation(a.home(), rec)
}

func (a *ActivationRunner) fact(rec *ActivationRecord, name string, fn func(*ActivationFact) error) error {
	f := ActivationFact{IntentAt: time.Now().UTC()}
	rec.Facts[name] = f
	if err := saveActivation(a.home(), rec); err != nil {
		return err
	}
	err := fn(&f)
	f.FinishedAt = time.Now().UTC()
	if err != nil {
		f.FailureCode = "activation_" + name + "_failed"
	}
	rec.Facts[name] = f
	if saveErr := saveActivation(a.home(), rec); saveErr != nil {
		return saveErr
	}
	if err != nil {
		return activationError(f.FailureCode)
	}
	return nil
}

func (a *ActivationRunner) command(rec *ActivationRecord, name string, fn func() (managedruntime.ActionResult, error)) error {
	return a.fact(rec, name, func(f *ActivationFact) error { result, err := fn(); f.Command = &result; return err })
}

func activationFailureCode(err error) string {
	if e, ok := valise.As(err); ok {
		return e.Code
	}
	return "activation_failed"
}
