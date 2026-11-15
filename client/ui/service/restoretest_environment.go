package service

import (
	"context"
	"slices"
	"valise/client/ssh"

	"valise/core/environment"
	"valise/core/identity"
	"valise/core/revive"
	"valise/core/valise"
	"valise/core/vault"
)

func (s *Service) prepareEnvironmentTest(ctx context.Context, p *RevivePlan, selected SelectedRestoreTest) error {
	if err := s.prepareEnvironmentMaterials(ctx, p, selected); err != nil {
		return err
	}
	if p.testMaterials != nil {
		if selected.ReviewMaterials == nil {
			return valise.New("confirmation_required", "完整测试需要确认范围外内容的备份来源", "从健康或旅程页查看本次组合清单后继续")
		}
		if !selected.ReviewMaterials(*p.testMaterials) {
			return valise.New("cancelled", "已取消完整测试，原 Agent 未改动", "")
		}
	}
	return nil
}

// Preparing metadata for a read-only review never asks or supplies a human
// execution confirmation. Tests obtain that confirmation separately above.
func (s *Service) prepareEnvironmentMaterials(ctx context.Context, p *RevivePlan, selected SelectedRestoreTest) error {
	h, files, err := p.vault.Snapshot(selected.Snapshot.SnapshotID, p.ident)
	if err != nil {
		return err
	}
	if h.Format != vault.FormatSnapshot || h.Runtime == nil || h.Runtime.Installation == nil || len(h.Runtime.Dependencies) == 0 ||
		h.ProfileDoc == nil || h.ProfileDoc.Environments == nil || h.Name != "" {
		return valise.New("environment_source_unavailable", "所选备份缺少当前适配所需的完整环境来源", "这个备份仍可校验文件；完整测试还需要已记录运行时来源的全量备份和对应的运行环境资料")
	}
	p.Plan.TestOnly = true
	p.Plan.IsolatedTest = &environment.TestInput{VaultID: selected.Snapshot.VaultID, HeaderDigest: selected.Source.Copy.HeaderDigest, Header: *h, Files: *files}
	if h.Tier != vault.TierFull && h.Tier != vault.TierRaw {
		caps, err := s.BackupCapabilities(ctx, p.Target)
		if err != nil {
			return err
		}
		if !slices.Contains(caps, environment.TestCompositionCapability) {
			return valise.New("unsupported_capability", "这个 porter 尚不支持组合非全量备份的完整测试", "更新 porter 后重新准备，或选择文件校验")
		}
		base, location, partial, err := s.selectTestBaseline(ctx, p)
		if err != nil {
			return err
		}
		if err := composeTestFiles(p, base); err != nil {
			return err
		}
		p.testMaterials = &TestMaterialReview{TargetTime: h.CreatedAt, TargetTier: h.Tier, Baseline: &environment.DataOrigin{Snapshot: identity.SnapshotRef{VaultID: p.Plan.IsolatedTest.Baseline.VaultID, SnapshotID: base.Plan.SnapshotID}, Time: base.Snapshot.CreatedAt}, BaselineLocation: location, Partial: partial, Files: p.Files, Bytes: p.Bytes}
		for _, f := range p.files {
			if f.Category == "secrets" && p.testDataSources[f.Root+"\x00"+f.Path] != nil {
				p.testMaterials.BaselineCredentials = true
			}
		}
	}
	return nil
}

func (s *Service) testEnvironment(ctx context.Context, p *RevivePlan, result *RestoreTest) error {
	report(ctx, "test_environment", 0, 0)
	transport, done, err := s.adminTransport(ctx, p.Target)
	if err != nil {
		return err
	}
	defer done()
	found, err := s.porterOn(ctx, transport, false)
	if err != nil {
		return err
	}
	client, hello, err := ssh.DialEnv(ctx, transport, found.Path, s.Version, nil)
	if err != nil {
		return err
	}
	defer client.Close()
	if hello == nil || !slices.Contains(hello.Capabilities, environment.TestCapability) {
		return valise.New("unsupported_capability", "管理连接上的 porter 尚不支持完整隔离测试", "更新 porter 后重新准备，不会改做文件测试")
	}
	if p.Plan.IsolatedTest.Baseline != nil && !slices.Contains(hello.Capabilities, environment.TestCompositionCapability) {
		return valise.New("unsupported_capability", "管理连接上的 porter 不支持已确认的组合测试", "更新 porter 后重新准备，不会忽略底座或换成文件测试")
	}
	var tested environment.TestResult
	if err := client.Call(ctx, environment.MethodTest, revive.OpParams{OpID: p.Plan.OpID}, &tested); err != nil {
		return err
	}
	result.Environment = &tested
	if tested.State != "finished" || tested.FailureCode != "" || !tested.CandidateBuilt || !tested.Probe.Verified() {
		return valise.New("environment_test_incomplete", "完整隔离测试尚未通过", "查看原测试记录及收尾状态，不要重复执行原操作")
	}
	return nil
}
