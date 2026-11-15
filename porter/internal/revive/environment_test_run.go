package revive

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"

	"valise/core/environment"
	"valise/core/identity"
	"valise/core/profile"
	"valise/core/protection"
	"valise/core/revive"
	"valise/core/valise"
	"valise/core/vault"
	"valise/porter/internal/backuprun"
	porterenroll "valise/porter/internal/enroll"
	"valise/porter/internal/environments"
	porteridentity "valise/porter/internal/identity"
	"valise/porter/internal/isolated"
)

func testOnlyError() error {
	return valise.New("test_only", "这是备份测试，不能替换当前实例或启停原服务", "测试结束后查看结果；需要恢复时请另外发起恢复")
}

func validateTestPlan(p revive.Plan) error {
	if p.IsolatedTest != nil && !p.TestOnly {
		return testOnlyError()
	}
	if p.TestOnly && (p.Mode != revive.ModeRollback || p.Carry || p.Body.Action != revive.BodyKeep || len(p.Body.Dirs) != 0 ||
		p.Unit != "" || p.InstallService || p.Identity != nil || p.Record != nil || len(p.Keep) != 0 || len(p.Supersedes) != 0) {
		return testOnlyError()
	}
	return nil
}

func testFailure(code string) error {
	reason, next := "所选备份暂时无法完成隔离重建", "可先校验文件；原 Agent 保持运行"
	switch code {
	case "environment_baseline_changed":
		reason = "补齐内容的全量备份与所选时间点不兼容，或来源已变化"
		next = "重新读取备份位置后再准备测试，不会用其他版本或当前目录补齐"
	case "environment_foundation_unavailable":
		reason = "没有适用的全量底座，按记录版本重建基础内容的适配尚未完成"
		next = "这个时间点仍可校验文件；完整测试需要可用的兼容全量备份或后续版本重建适配"
	case "environment_snapshot_unsupported":
		reason = "这个备份尚不支持完整隔离测试"
		next = "先选择文件校验；目前完整测试需要已记录运行时来源的默认实例全量备份"
	case "environment_profile_changed", "environment_registration_changed", "environment_layout_changed":
		reason = "备份记录的安装资料或位置与当前接入记录不一致"
		next = "先校验文件；核对当前接入资料后重新准备，不使用当前程序猜测旧环境"
	case "environment_identity_unknown":
		reason = "无法核实这个备份属于当前 Agent 的哪次接入"
		next = "先校验文件；已有的旧备份不会被改写，也不会按同名实例猜测身份"
	case "environment_readiness_unsupported":
		reason = "这份运行环境资料没有可用的官方就绪检查"
		next = "先选择文件校验，等待这个版本的完整测试适配"
	case "environment_source_unavailable":
		reason = "当前程序或运行时与备份中记录的来源不吻合"
		next = "先校验文件；这个时间点需要按记录版本重建程序，不能借用当前版本"
	case "environment_stage_changed":
		reason = "测试暂存中的文件未通过校验"
		next = "清理本次测试后换一个备份再试"
	case "environment_space_low":
		reason = "可用磁盘空间不足以同时准备独立程序和测试数据"
		next = "释放磁盘空间后重新测试，或选择只校验文件"
	case "environment_build_failed":
		reason = "独立程序与备份数据未能组成可测试环境"
		next = "查看分项结果；先校验文件或选择其他备份，不会改动原 Agent"
	case "environment_gateway_failed":
		reason = "隔离环境的网关未通过就绪检查"
		next = "查看网关与进程收尾结果；这不影响原 Agent 继续运行"
	case "environment_stop_unverified":
		reason = "尚未核实测试进程已经全部停止"
		next = "保留本次测试记录和暂存，先核实原操作，不要重新发起测试"
	case "environment_workspace":
		reason = "无法建立或核实本次测试的私有暂存目录"
		next = "检查服务器磁盘与 Valise 状态目录权限后再核实原测试"
	}
	return valise.New(code, reason, next)
}

// TestEnvironment consumes a frozen, verified test stage exactly once. Only an
// independently authenticated management session may invoke it. The runner
// never touches a live directory, unit or environment registry.
func (r *Runner) TestEnvironment(ctx context.Context, op string) (*environment.TestResult, error) {
	j, err := r.Store.Load(op)
	if err != nil {
		return nil, err
	}
	if !j.Plan.TestOnly || j.Plan.IsolatedTest == nil || validateTestPlan(j.Plan) != nil || j.State != revive.StatePlanned || !j.StageVerified || !j.ManifestSaved {
		return nil, testOnlyError()
	}
	if j.EnvironmentTest != nil {
		// A repeated request returns only the original durable facts. In
		// particular "probing" after a crash is never silently retried.
		return j.EnvironmentTest, nil
	}
	in, source, check, err := r.environmentTestInput(ctx, j)
	if err != nil {
		return nil, err
	}
	// Recheck the original immutable stage before recording execution intent.
	verified, err := StageVerify(r.Store, revive.StageVerifyParams{OpID: op, Manifest: j.Manifest})
	if err != nil || len(verified.Problems) != 0 {
		return nil, testFailure("environment_stage_changed")
	}
	j.EnvironmentTest = &environment.TestResult{State: "preparing"}
	if b := j.Plan.IsolatedTest.Baseline; b != nil {
		j.EnvironmentTest.Baseline = &environment.DataOrigin{Snapshot: identity.SnapshotRef{VaultID: b.VaultID, SnapshotID: b.Header.SnapshotID}, Time: b.Header.CreatedAt}
	}
	if err := r.Store.Save(j); err != nil {
		return nil, err
	}
	finish := func(code string, cause error) (*environment.TestResult, error) {
		j.EnvironmentTest.State, j.EnvironmentTest.FailureCode = "finished", code
		if err := r.Store.Save(j); err != nil {
			return j.EnvironmentTest, err
		}
		if cause != nil {
			return j.EnvironmentTest, testFailure(code)
		}
		return j.EnvironmentTest, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	parent, err := r.testDirectory(op, true)
	if err != nil {
		return finish("environment_workspace", err)
	}
	budgetPlan, err := r.composeEnvironmentTest(ctx, j, &in)
	if err != nil {
		return finish("environment_baseline_changed", err)
	}
	budgetData := vault.Files{Format: vault.FormatFiles}
	for _, entry := range budgetPlan.Entries {
		if entry.File != nil {
			budgetData.Files = append(budgetData.Files, *entry.File)
		}
		if entry.Link != nil {
			budgetData.Symlinks = append(budgetData.Symlinks, *entry.Link)
		}
	}
	source.CheckSpace = func(program environments.ProgramMaterial) error { return checkTestSpace(parent, program, budgetData) }
	archive, err := environments.FreezeInstalledNPM(ctx, parent, source, j.Plan.IsolatedTest.Header.Runtime)
	if err != nil {
		code := "environment_source_unavailable"
		if e, ok := valise.As(err); ok && e.Code == "environment_space_low" {
			code = e.Code
		}
		return finish(code, err)
	}
	defer archive.File.Close()
	in.Layout.RuntimeExecutable = archive.RuntimeExecutable
	in.Target.Source.LayoutDigest = environments.LayoutDigest(in.Layout, in.Overrides)
	if in.Baseline != nil {
		in.Baseline.Source.LayoutDigest = in.Target.Source.LayoutDigest
	}
	in.Program, in.Archive = archive.Material, archive.File
	candidate, err := environments.Build(ctx, parent, in)
	if err != nil {
		return finish("environment_build_failed", err)
	}
	j.EnvironmentTest.CandidateBuilt = candidate.Receipt.CandidateBuilt
	j.EnvironmentTest.ProgramSHA256, j.EnvironmentTest.DataPlanDigest = candidate.Receipt.ProgramSHA256, candidate.Receipt.DataPlanDigest
	j.EnvironmentTest.State, j.EnvironmentTest.ProbeAttempted = "probing", true
	if err := r.Store.Save(j); err != nil {
		return j.EnvironmentTest, err
	}
	probe, err := isolated.GatewayProbe(ctx, candidate.Workspace, candidate.Spec, check)
	j.EnvironmentTest.Probe = probe
	if err != nil || !probe.Verified() {
		if err == nil {
			err = errors.New("gateway readiness not verified")
		}
		return finish("environment_gateway_failed", err)
	}
	return finish("", nil)
}

// environmentTestInput derives executable knowledge from the accepted dossier,
// and identity from a durable backup binding, never a request's verified flag.
// First transport version supports complete full/raw manifests; supplemental
// historical bases and recorded-version installation are separate adapters.
func (r *Runner) environmentTestInput(ctx context.Context, j *revive.Journal) (environments.BuildInput, environments.InstalledSource, isolated.ProbeCheck, error) {
	var in environments.BuildInput
	var source environments.InstalledSource
	var check isolated.ProbeCheck
	bad := func(code string) (environments.BuildInput, environments.InstalledSource, isolated.ProbeCheck, error) {
		return in, source, check, testFailure(code)
	}
	q, p := j.Plan.IsolatedTest, j.Plan
	h := q.Header
	if !vault.ValidID(q.VaultID) || len(q.HeaderDigest) != 64 || h.Format != vault.FormatSnapshot || h.SnapshotID != p.SnapshotID || h.InstanceID != p.InstanceID || h.Tier != p.Tier ||
		vault.TierCategories[h.Tier] == nil || h.Runtime == nil || h.Runtime.Installation == nil || h.Name != "" || q.Files.Format != vault.FormatFiles {
		return bad("environment_snapshot_unsupported")
	}
	d, accepted, err := porterenroll.ReadInstallation(r.Store.Home, p.InstanceID)
	if err != nil || d == nil || accepted == nil || accepted.Environments == nil || environments.Digest(*accepted) != environments.Digest(p.Profile) ||
		environments.Digest(h.ProfileDoc) != environments.Digest(*accepted) || h.Profile.ID != accepted.ID || h.Profile.Version != accepted.ProfileVersion ||
		h.Runtime.Home.Path != r.Store.Home || h.Runtime.Installation.InstallID != d.InstallID || h.Runtime.Installation.DefaultInstanceID != p.InstanceID ||
		environments.Digest(h.Provenance) != environments.Digest(d.Provenance) {
		return bad("environment_profile_changed")
	}
	rec, err := porterenroll.ReadRecord(r.Store.Home, p.InstanceID)
	if err != nil || rec == nil || rec.Superseded != nil || rec.Name != "" || !slices.Equal(rec.LocalOverrides, p.Overrides) ||
		!slices.Equal(h.LocalOverrides, p.Overrides) || len(rec.Roots) != len(p.Roots) || len(h.Roots) != len(rec.Roots) || len(h.Runtime.Roots) != len(rec.Roots) {
		return bad("environment_registration_changed")
	}
	for _, root := range p.Roots {
		if rec.Roots[root.ID] != root.Path || h.Roots[root.ID] != root.Path || h.Runtime.Roots[root.ID].Path != root.Path {
			return bad("environment_layout_changed")
		}
	}
	ref := identity.SnapshotRef{VaultID: q.VaultID, SnapshotID: h.SnapshotID}
	binding, err := (backuprun.Store{Home: r.Store.Home}).SnapshotBinding(ctx, protection.SnapshotBindingParams{SnapshotRef: ref})
	current, currentErr := porteridentity.Read(r.Store.Home, p.InstanceID)
	if err != nil || currentErr != nil || binding == nil || binding.State != "bound" || binding.AgentRef == nil || current.Agent == nil || *binding.AgentRef != current.Agent.Ref {
		return bad("environment_identity_unknown")
	}
	revision := ""
	if d.Provenance.Git != nil {
		revision = d.Provenance.Git.Commit
	}
	check, err = isolated.CheckForRuntime(*accepted, h.Runtime.Home.Path, h.Roots[profile.StateRoot], revision)
	if err != nil {
		return bad("environment_readiness_unsupported")
	}
	source = environments.InstalledSource{Profile: *accepted, Overrides: p.Overrides,
		Layout: environments.Layout{Home: h.Runtime.Home.Path, Roots: rec.Roots}, Origin: d.Provenance, MaxBytes: 8 << 30}
	in = environments.BuildInput{Profile: *accepted, Overrides: p.Overrides, Layout: source.Layout, MaxBytes: 16 << 30,
		Target: environment.Material{Files: q.Files, Source: environment.Source{Agent: *binding.AgentRef, Snapshot: ref, Time: h.CreatedAt, Tier: h.Tier,
			ProfileDigest: environments.Digest(*accepted), ProgramDigest: environments.ProgramSourceDigest(d.Provenance), LayoutDigest: environments.LayoutDigest(source.Layout, p.Overrides), Verified: true}}}
	plan, err := r.composeEnvironmentTest(ctx, j, &in)
	if err != nil {
		return in, source, check, err
	}
	allowed := map[string]identity.SnapshotRef{}
	for _, entry := range plan.Entries {
		allowed[entry.Root+"\x00"+entry.Path] = entry.Source
	}
	in.OpenData = func(ctx context.Context, requested identity.SnapshotRef, root, path string) (io.ReadCloser, error) {
		if expected, ok := allowed[root+"\x00"+path]; !ok || requested != expected || ctx.Err() != nil {
			return nil, environments.ErrData
		}
		name := root + "/" + path
		for _, rp := range p.Roots {
			if rp.ID == root && rp.SwapRoot != "" {
				name = rp.SwapRoot + "/" + filepath.ToSlash(filepath.Join(rp.Rel, path))
			}
		}
		dest, err := stageDest(j, name)
		if err != nil {
			return nil, environments.ErrData
		}
		base := rootOf(j, dest)
		dir, err := os.OpenRoot(base)
		if err != nil {
			return nil, environments.ErrData
		}
		defer dir.Close()
		rel, err := filepath.Rel(base, dest)
		if err != nil {
			return nil, environments.ErrData
		}
		return dir.Open(rel)
	}
	return in, source, check, nil
}
