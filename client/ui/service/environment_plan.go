package service

import (
	"context"
	"math"
	"strings"
	"time"

	"valise/core/environment"
	"valise/core/journey"
	"valise/core/protection"
	"valise/core/revive"
	"valise/core/valise"
)

// PreviewEnvironmentRestore decrypts only the exact selected metadata, then
// obtains read-only source/process/space facts. It creates no task or stage.
func (s *Service) PreviewEnvironmentRestore(ctx context.Context, req RestoreRequest) (*revive.EnvironmentPlan, error) {
	p, err := s.PrepareParallelRestore(ctx, req)
	if err != nil {
		return nil, err
	}
	defer p.Close()
	view := p.View
	return &view, nil
}

// PrepareParallelRestore retains the exact decrypted sources only in the
// human process. It is still read-only; RunParallelPreparation is separate.
func (s *Service) PrepareParallelRestore(ctx context.Context, req RestoreRequest) (*PreparedParallelRestore, error) {
	o := req.Options
	if o.Mode != revive.ModeRollback || o.Target == "" || o.Target != o.From || req.Snapshot.SnapshotID != o.SnapshotID || req.Source.Copy.HeaderDigest == "" {
		return nil, restoreError("source_invalid", "恢复条件必须绑定当前 Agent 的一个明确时间点和副本")
	}
	allowed := false
	for _, src := range s.RestoreSources(journey.TimelinePoint{SnapshotRef: req.Snapshot, Copies: []journey.Copy{req.Source.Copy}}) {
		allowed = allowed || src.Spec == req.Source.Spec
	}
	if !allowed || req.Source.Spec == "" && req.Source.Copy.LocationRef.ID != o.From {
		return nil, restoreError("source_invalid", "这个副本还不能读取，或与所选服务器不一致")
	}
	instance := req.Source.Copy.Metadata.InstanceID
	settings, err := s.InstanceEnvironmentSettings(ctx, o.Target, instance)
	if err != nil {
		return nil, err
	}
	if settings.Busy || settings.Mode == environment.Direct {
		return nil, valise.New("environment_plan_changed", "当前恢复方式或操作状态不适合这次并行预览", "重新读取恢复设置和原操作后，再查看条件")
	}
	if err := s.currentRestoreRoots(ctx, &o, instance); err != nil {
		return nil, err
	}
	o.Source, o.testOnly, o.AllowDraft, o.AllowSnapshotProfile = req.Source.Spec, true, true, true
	p, err := s.planRevive(ctx, o, "")
	if err != nil {
		return nil, err
	}
	retained := false
	defer func() {
		if !retained {
			p.Close()
		}
	}()
	selected := SelectedRestoreTest{Target: o.Target, Instance: instance, Snapshot: req.Snapshot, Source: req.Source, Depth: "isolated"}
	if err := selected.verify(p); err != nil {
		return nil, err
	}
	if err := s.prepareEnvironmentMaterials(ctx, p, selected); err != nil {
		return nil, err
	}
	params := revive.EnvironmentPlanParams{Agent: settings.Agent, SettingsDigest: settings.Digest, Input: *p.Plan.IsolatedTest}
	var view revive.EnvironmentPlan
	if err := s.call(ctx, o.Target, environment.MethodPlan, params, &view); err != nil {
		return nil, err
	}
	if err := validateEnvironmentPlan(view, params); err != nil {
		return nil, err
	}
	if view.Mode != settings.Mode {
		return nil, testSelectionError()
	}
	trust, err := s.restoreTrust(o.Target)
	if err != nil {
		return nil, err
	}
	retained = true
	return &PreparedParallelRestore{View: view, Materials: p.testMaterials, RequiresCredentials: p.RestoreSecret || p.testMaterials != nil && p.testMaterials.BaselineCredentials,
		reuseRequired: view.Reuse != nil,
		plan:          p, params: params, request: req, trust: trust, Expires: time.Now().Add(10 * time.Minute)}, nil
}

func validateEnvironmentPlan(v revive.EnvironmentPlan, p revive.EnvironmentPlanParams) error {
	if v.ManagedSource {
		valid := func(ref *environment.Ref) bool {
			return ref != nil && (ref.Slot == environment.A || ref.Slot == environment.B) && ref.Revision > 0
		}
		if !valid(v.Original) || !valid(v.Candidate) || v.Original.Slot == v.Candidate.Slot {
			return valise.New("environment_plan_invalid", "后续恢复条件缺少明确的当前环境与目标修订", "重新读取这个 Agent 的恢复条件")
		}
	} else if v.Original != nil || v.Candidate != nil {
		return valise.New("environment_plan_invalid", "原生接入条件与运行环境引用不一致", "重新读取恢复条件")
	}
	if u := v.Reuse; u != nil {
		if !v.ManagedSource || u.Target.Slot != v.Candidate.Slot || u.Target.Revision == ^uint64(0) || u.Target.Revision+1 != v.Candidate.Revision {
			return testSelectionError()
		}
		for _, item := range u.Items {
			if item.Files < 0 || item.Bytes < 0 || item.Kind != "data" && item.Kind != "program" && item.Kind != "preparation" {
				return testSelectionError()
			}
		}
	}
	validHash := func(s string) bool {
		return len(s) == 64 && strings.IndexFunc(s, func(r rune) bool { return !(r >= 'a' && r <= 'f' || r >= '0' && r <= '9') }) < 0
	}
	if v.Agent != p.Agent || v.Snapshot.VaultID != p.Input.VaultID || v.Snapshot.SnapshotID != p.Input.Header.SnapshotID || v.InputDigest != planInputDigest(p) ||
		v.Tier != p.Input.Header.Tier || !v.CreatedAt.Equal(p.Input.Header.CreatedAt) || v.Mode != environment.Parallel && v.Mode != environment.Compact ||
		!validHash(v.Digest) || !v.NativeRunning || !v.ProgramMatches || v.NativeUnit == "" || v.ProgramVersion == "" || v.ProgramFiles <= 0 || v.ProgramBytes < 0 || v.Files < 0 || v.Bytes < 0 ||
		v.PhysicalRoots <= 0 || v.LogicalRoots < v.PhysicalRoots || len(v.Space) == 0 {
		return valise.New("environment_plan_invalid", "服务器返回的恢复条件与选定时间点不一致", "重新选择时间点和副本，再查看恢复条件")
	}
	if source := p.Input.Header.Provenance.NPM; source != nil && source.Version != v.ProgramVersion {
		return testSelectionError()
	}
	for _, space := range v.Space {
		if space.NeedBytes > math.MaxInt64 || space.FreeBytes > math.MaxInt64 {
			return testSelectionError()
		}
	}
	if (v.Baseline == nil) != (p.Input.Baseline == nil) {
		return testSelectionError()
	}
	if b := p.Input.Baseline; b != nil && (v.Baseline.Snapshot.VaultID != b.VaultID || v.Baseline.Snapshot.SnapshotID != b.Header.SnapshotID || !v.Baseline.Time.Equal(b.Header.CreatedAt)) {
		return testSelectionError()
	}
	return nil
}

func planInputDigest(p revive.EnvironmentPlanParams) string {
	d, _ := protection.Digest(p)
	return d
}
