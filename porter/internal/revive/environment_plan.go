package revive

import (
	"context"
	"runtime"
	"slices"

	"valise/core/environment"
	"valise/core/profile"
	"valise/core/revive"
	"valise/core/valise"
	"valise/porter/internal/activationrecord"
	porterenroll "valise/porter/internal/enroll"
	"valise/porter/internal/environments"
	"valise/porter/internal/managedruntime"
)

func planError(code string) error {
	reason, next := "这个时间点暂不能准备并行恢复", "仍可校验备份文件；核实当前 Agent 的接入资料和备份来源后重新查看"
	switch code {
	case "environment_plan_changed":
		reason, next = "当前 Agent 或恢复设置已经改变，原条件已失效", "重新读取 Agent 与恢复设置后，再查看这个时间点的恢复条件"
	case "environment_plan_platform":
		reason, next = "这台服务器尚无可用的并行运行适配", "仍可校验备份文件；等待此系统的运行适配后再准备并行恢复"
	case "environment_plan_native":
		reason, next = "无法核实这个 Agent 的原生服务及实际运行程序", "检查当前服务和接入资料，确认原生启动方式后重新查看条件"
	case "environment_plan_shared":
		reason, next = "当前启动入口或目录还被其他 Agent 使用", "先核实共享服务影响；当前构建尚不能并行切换这个共享环境"
	case "environment_plan_source":
		reason, next = "原程序与所选备份记录的来源不吻合，不能用当前版本替代旧版本", "仍可校验文件；选择来源吻合的备份，或等待记录版本的重建适配"
	case "environment_plan_busy":
		reason, next = "这个 Agent 已有运行环境记录或未完成的恢复", "回到原恢复记录核实当前状态，再准备下一项恢复"
	case "environment_plan_reuse_pending":
		reason, next = "另一运行环境还保留着内容，本构建尚未接通它的正常复用", "当前环境可以继续使用；等待后续恢复的槽位复用接入后再准备"
	case "environment_plan_input":
		reason, next = "所选备份缺少当前适配所需的完整运行环境记录", "仍可校验文件；使用记录了运行时来源的备份，或等待对应版本重建适配"
	case "environment_plan_space":
		reason, next = "无法核实准备新环境所需的额外磁盘空间", "检查服务器磁盘状态和登记目录后重新查看条件"
	}
	return valise.New(code, reason, next)
}

func (r *Runner) PlanEnvironment(ctx context.Context, p revive.EnvironmentPlanParams) (*revive.EnvironmentPlan, error) {
	return r.planEnvironment(ctx, p, managedruntime.Controller{Home: r.Store.Home})
}

type environmentPlanFacts struct {
	Managed     *activationrecord.Source
	Registry    *environment.Registry
	Next        environment.Ref
	Reuse       *preparationReuse
	View        *revive.EnvironmentPlan
	Plan        revive.Plan
	Native      managedruntime.NativeRef
	Source      environments.InstalledSource
	Material    environments.ProgramMaterial
	Data        environment.DataPlan
	Directories []revive.ManagedRoot
}

func (r *Runner) planEnvironment(ctx context.Context, p revive.EnvironmentPlanParams, control managedruntime.Controller) (*revive.EnvironmentPlan, error) {
	facts, err := r.inspectEnvironmentPlan(ctx, p, control)
	if err != nil {
		return nil, err
	}
	return facts.View, nil
}

// planEnvironment only observes. In particular it does not initialize an
// environment registry, create an archive/stage, probe a gateway or stop A.
func (r *Runner) inspectEnvironmentPlan(ctx context.Context, p revive.EnvironmentPlanParams, control managedruntime.Controller) (*environmentPlanFacts, error) {
	if runtime.GOOS != "linux" {
		return nil, planError("environment_plan_platform")
	}
	if err := porterenroll.ValidateIdentity(ctx, r.Store.Home, p.Agent); err != nil {
		return nil, err
	}
	settings, err := (environments.Store{Home: r.Store.Home}).Settings(p.Agent)
	if err != nil {
		return nil, err
	}
	if settings.Digest != p.SettingsDigest || settings.Mode == environment.Direct {
		return nil, planError("environment_plan_changed")
	}
	if settings.Busy {
		return nil, planError("environment_plan_busy")
	}
	if settings.Managed {
		return r.inspectManagedEnvironmentPlan(ctx, p, control, settings)
	}
	d, accepted, err := porterenroll.ReadInstallation(r.Store.Home, p.Agent.InstanceID)
	if err != nil || d == nil || accepted == nil || accepted.Environments == nil || d.InstallID != p.Agent.InstallID {
		return nil, planError("environment_plan_input")
	}
	record := d.Record(p.Agent.InstanceID)
	if record == nil || record.Name != "" || record.Superseded != nil || p.Input.Header.Runtime == nil {
		return nil, planError("environment_plan_input")
	}
	roots, err := revive.CanonicalRoots(record.Roots)
	if err != nil {
		return nil, planError("environment_plan_input")
	}
	rootFacts, err := planDirectoryFacts(roots)
	if err != nil {
		return nil, planError("environment_plan_changed")
	}
	plan := revive.Plan{Mode: revive.ModeRollback, InstanceID: p.Agent.InstanceID, SnapshotID: p.Input.Header.SnapshotID, Tier: p.Input.Header.Tier, Profile: *accepted, Overrides: record.LocalOverrides, Roots: roots, IsolatedTest: &p.Input}
	j := &revive.Journal{Plan: plan}
	in, source, check, err := r.environmentTestInput(ctx, j)
	if err != nil {
		return nil, planningInputError(err)
	}
	if check.Offline != nil {
		return nil, activationError("activation_readiness_unsupported")
	}
	unit, err := planningUnit(*accepted)
	if err != nil {
		return nil, err
	}
	if err := r.planShared(ctx, p.Agent.InstanceID, unit, roots); err != nil {
		return nil, err
	}
	native, before, err := control.InspectNative(ctx, *accepted, unit)
	if err != nil {
		return nil, planError("environment_plan_native")
	}
	observed, err := environments.ObserveInstalledNPM(ctx, source)
	if err != nil || environments.Digest(observed.Dependencies) != environments.Digest(p.Input.Header.Runtime.Dependencies) {
		return nil, planError("environment_plan_source")
	}
	if observed.RuntimeExecutable != "" && observed.RuntimeExecutable != native.Executable {
		return nil, planError("environment_plan_source")
	}
	match := false
	for _, f := range observed.Material.Files {
		if source.Layout.Home+"/"+f.Path == native.Executable && f.SHA256 == native.ExecutableSHA256 {
			match = true
		}
	}
	if !match {
		return nil, planError("environment_plan_source")
	}
	data, err := r.composeEnvironmentTest(ctx, j, &in)
	if err != nil {
		return nil, err
	}
	space, err := planEnvironmentSpace(r.Store.Home, roots, observed.Material, data)
	if err != nil {
		return nil, err
	}
	afterNative, after, err := control.InspectNative(ctx, *accepted, unit)
	if err != nil || native != afterNative || before.MainPID != after.MainPID || before.KernelStart != after.KernelStart {
		return nil, planError("environment_plan_changed")
	}
	if err := porterenroll.ValidateIdentity(ctx, r.Store.Home, p.Agent); err != nil {
		return nil, err
	}
	final, err := (environments.Store{Home: r.Store.Home}).Settings(p.Agent)
	if err != nil || final.Digest != settings.Digest {
		return nil, planError("environment_plan_changed")
	}
	finalRoots, err := planDirectoryFacts(roots)
	if err != nil || !slices.Equal(rootFacts, finalRoots) {
		return nil, planError("environment_plan_changed")
	}
	view := &revive.EnvironmentPlan{Agent: p.Agent, Snapshot: in.Target.Source.Snapshot, CreatedAt: p.Input.Header.CreatedAt, Tier: p.Input.Header.Tier, Mode: settings.Mode,
		InputDigest: environments.Digest(p), NativeRunning: after.Running, NativeUnit: unit.Unit, EnableOnBoot: native.EnableOnBoot, ProgramMatches: true,
		ProgramFiles: len(observed.Material.Files), Files: len(data.Entries), LogicalRoots: len(roots), Space: space}
	for _, root := range roots {
		if root.SwapRoot == "" {
			view.PhysicalRoots++
		}
	}
	for _, f := range observed.Material.Files {
		view.ProgramBytes += f.Size
	}
	for _, e := range data.Entries {
		if e.File != nil {
			view.Bytes += e.File.Size
		}
	}
	if b := p.Input.Baseline; b != nil {
		view.Baseline = &environment.DataOrigin{Snapshot: in.Baseline.Source.Snapshot, Time: b.Header.CreatedAt}
	}
	version := ""
	if d.Provenance.NPM != nil {
		version = d.Provenance.NPM.Version
	}
	view.ProfileUntested = !accepted.EnvironmentTested(version)
	view.ProgramVersion = version
	view.Digest = environments.Digest(struct {
		Input       string
		Native      managedruntime.NativeRef
		Material    string
		Roots       []revive.RootPlan
		Directories []revive.ManagedRoot
	}{view.InputDigest, native, environments.Digest(observed.Material), roots, rootFacts})
	return &environmentPlanFacts{View: view, Plan: plan, Native: native, Source: source, Material: observed.Material, Data: data, Directories: rootFacts}, nil
}

func planDirectoryFacts(roots []revive.RootPlan) ([]revive.ManagedRoot, error) {
	bases := map[string]string{}
	for _, r := range roots {
		bases[r.ID] = r.Path
	}
	var out []revive.ManagedRoot
	for _, r := range roots {
		base, rel := r.Path, ""
		if r.SwapRoot != "" {
			base, rel = bases[r.SwapRoot], r.Rel
		}
		fact, err := managedLogicalIdentity(r.ID, base, rel)
		if err != nil {
			return nil, err
		}
		out = append(out, fact)
	}
	return out, nil
}

func planningInputError(err error) error {
	if e, ok := valise.As(err); ok {
		if e.Code == "environment_snapshot_unsupported" {
			return planError("environment_plan_input")
		}
		return valise.New(e.Code, e.Reason, "仍可校验备份文件；核实这个时间点的资料、接入身份和来源后再查看恢复条件")
	}
	return err
}

func planningUnit(p profile.Profile) (profile.ServiceUnit, error) {
	if p.Service == nil {
		return profile.ServiceUnit{}, planError("environment_plan_native")
	}
	if p.Environments != nil && p.Environments.RuntimeExecutable != nil {
		u := p.Environments.RuntimeExecutable.Service
		if u.Scope == "user" && slices.Contains(p.Service.Candidates, u) {
			return u, nil
		}
	}
	if len(p.Service.Candidates) == 1 && p.Service.Candidates[0].Scope == "user" {
		return p.Service.Candidates[0], nil
	}
	return profile.ServiceUnit{}, planError("environment_plan_native")
}

func (r *Runner) planShared(ctx context.Context, instance string, unit profile.ServiceUnit, roots []revive.RootPlan) error {
	records, err := porterenroll.ListRecords(r.Store.Home)
	if err != nil {
		return err
	}
	for _, other := range records {
		if err := ctx.Err(); err != nil {
			return err
		}
		if other.InstanceID == instance || other.Superseded != nil {
			continue
		}
		for _, path := range other.Roots {
			for _, root := range roots {
				if within(path, root.Path) || within(root.Path, path) {
					return planError("environment_plan_shared")
				}
			}
		}
		p, err := porterenroll.ReadProfile(r.Store.Home, other.InstanceID)
		if err != nil || p == nil {
			return planError("environment_plan_shared")
		}
		if p.Service != nil && slices.Contains(p.Service.Candidates, unit) {
			return planError("environment_plan_shared")
		}
	}
	return nil
}
