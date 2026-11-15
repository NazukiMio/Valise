package service

import (
	"context"
	"slices"
	"sort"
	"time"

	"valise/core/environment"
	"valise/core/identity"
	"valise/core/journey"
	"valise/core/profile"
	"valise/core/protection"
	"valise/core/revive"
	"valise/core/valise"
	"valise/core/vault"
)

type TestMaterialReview struct {
	BaselineCredentials bool                    `json:"baseline_credentials"`
	TargetTime          time.Time               `json:"target_time"`
	TargetTier          string                  `json:"target_tier"`
	Baseline            *environment.DataOrigin `json:"baseline,omitempty"`
	BaselineLocation    *journey.LocationRef    `json:"baseline_location,omitempty"`
	Partial             bool                    `json:"partial"`
	Files               int                     `json:"files"`
	Bytes               int64                   `json:"bytes"`
}

func foundationUnavailable() error {
	return valise.New("environment_foundation_unavailable", "没有找到适用的全量备份，按记录版本重建基础内容的适配尚未完成", "这个时间点仍可校验文件；不会从当前 Agent 或更新的备份中补齐")
}

func (s *Service) testSnapshotAgent(ctx context.Context, host string, ref identity.SnapshotRef) (*identity.AgentRef, error) {
	var binding protection.SnapshotBinding
	if err := s.call(ctx, host, protection.MethodSnapshotBinding, protection.SnapshotBindingParams{SnapshotRef: ref}, &binding); err != nil {
		return nil, err
	}
	if binding.State != "bound" || binding.AgentRef == nil {
		return nil, valise.New("environment_identity_unknown", "无法核实这个时间点与其他备份是否属于同一个 Agent", "仍可校验文件；完整测试不能按同名实例自动选择底座")
	}
	return binding.AgentRef, nil
}

func (s *Service) selectTestBaseline(ctx context.Context, p *RevivePlan) (*RevivePlan, *journey.LocationRef, bool, error) {
	q := p.Plan.IsolatedTest
	ref, err := s.testSnapshotAgent(ctx, p.Target, identity.SnapshotRef{VaultID: q.VaultID, SnapshotID: q.Header.SnapshotID})
	if err != nil {
		return nil, nil, false, err
	}
	p.testSourceAgent = ref
	var page JourneyPage
	if err := s.ScanTimelineInScope(ctx, TimelineScanRequest{ConnectionIDs: []string{p.Target}}, func(update TimelineUpdate) { page = update.Page }); err != nil {
		return nil, nil, false, err
	}
	points := slices.Clone(page.Items)
	sort.Slice(points, func(i, j int) bool {
		if points[i].CreatedAt.Equal(points[j].CreatedAt) {
			return points[i].SnapshotRef.SnapshotID > points[j].SnapshotRef.SnapshotID
		}
		return points[i].CreatedAt.After(points[j].CreatedAt)
	})
	for _, point := range points {
		if point.Scope.Tier != vault.TierFull || point.CreatedAt.After(q.Header.CreatedAt) || point.RecordedInstanceID != p.Plan.InstanceID || point.MetadataState == "conflict" {
			continue
		}
		sourceAgent, err := s.testSnapshotAgent(ctx, p.Target, identity.SnapshotRef{VaultID: point.SnapshotRef.VaultID, SnapshotID: point.SnapshotRef.SnapshotID})
		if ctx.Err() != nil {
			return nil, nil, page.Partial, ctx.Err()
		}
		if err != nil || sourceAgent == nil || *sourceAgent != *ref {
			continue
		}
		for _, source := range s.RestoreSources(point) {
			if source.Copy.HeaderDigest == "" || source.Copy.MetadataState == "conflict" {
				continue
			}
			o := ReviveOptions{Target: p.Target, SnapshotID: point.SnapshotRef.SnapshotID, Source: source.Spec, Mode: revive.ModeRollback, AllowDraft: true, AllowSnapshotProfile: true, testOnly: true, Roots: map[string]string{}}
			if source.Copy.LocationRef.Kind == "server_vault" {
				o.From = source.Copy.LocationRef.ID
			}
			for _, root := range p.Plan.Roots {
				o.Roots[root.ID] = root.Path
			}
			base, err := s.planRevive(ctx, o, "")
			if ctx.Err() != nil {
				return nil, nil, page.Partial, ctx.Err()
			}
			if err != nil {
				continue
			}
			selection := SelectedRestoreTest{Target: p.Target, Instance: p.Plan.InstanceID, Snapshot: point.SnapshotRef, Source: source}
			if selection.verify(base) != nil || len(base.Blockers) > 0 {
				base.Close()
				continue
			}
			h, files, err := base.vault.Snapshot(base.Plan.SnapshotID, base.ident)
			if err != nil || !environment.CompatibleTestBaseline(q.Header, *h) {
				base.Close()
				continue
			}
			q.Baseline = &environment.TestSnapshot{VaultID: point.SnapshotRef.VaultID, HeaderDigest: source.Copy.HeaderDigest, Header: *h, Files: *files}
			loc := source.Copy.LocationRef
			return base, &loc, page.Partial, nil
		}
	}
	return nil, nil, page.Partial, foundationUnavailable()
}

func composeTestFiles(p, base *RevivePlan) error {
	q := p.Plan.IsolatedTest
	if p.testSourceAgent == nil {
		return foundationUnavailable()
	}
	profileDigest, err := protection.Digest(q.Header.ProfileDoc)
	if err != nil {
		return err
	}
	programDigest, err := protection.Digest(q.Header.Runtime.Installation.RegisteredProvenance)
	if err != nil {
		return err
	}
	layoutDigest, err := protection.Digest(struct {
		Home      vault.RuntimeRoot
		Roots     map[string]vault.RuntimeRoot
		Overrides []profile.Override
	}{q.Header.Runtime.Home, q.Header.Runtime.Roots, q.Header.LocalOverrides})
	if err != nil {
		return err
	}
	source := environment.Source{Agent: *p.testSourceAgent,
		Snapshot: identity.SnapshotRef{VaultID: q.VaultID, SnapshotID: q.Header.SnapshotID}, Time: q.Header.CreatedAt, Tier: q.Header.Tier,
		ProfileDigest: profileDigest, ProgramDigest: programDigest, LayoutDigest: layoutDigest, Verified: true}
	old := source
	old.Snapshot, old.Time, old.Tier = identity.SnapshotRef{VaultID: q.Baseline.VaultID, SnapshotID: q.Baseline.Header.SnapshotID}, q.Baseline.Header.CreatedAt, vault.TierFull
	roots := []string{profile.StateRoot}
	for _, r := range p.Plan.Profile.Roots {
		roots = append(roots, r.ID)
	}
	classifier := profile.NewClassifier(&p.Plan.Profile, p.Plan.Overrides)
	data, err := environment.Compose(environment.Material{Source: source, Files: q.Files}, &environment.Material{Source: old, Files: q.Baseline.Files}, roots, func(root, path string) string { return classifier.Classify(root, path).Category })
	if err != nil {
		return err
	}
	p.files, p.links, p.Files, p.Bytes = nil, nil, 0, 0
	p.testDataSources = map[string]*RevivePlan{}
	for _, entry := range data.Entries {
		if entry.File != nil {
			p.files = append(p.files, *entry.File)
			p.Files++
			p.Bytes += entry.File.Size
			if entry.Source == old.Snapshot {
				p.testDataSources[entry.Root+"\x00"+entry.Path] = base
				p.sqlite[entry.Root+":"+entry.Path] = base.sqlite[entry.Root+":"+entry.Path]
			}
		}
		if entry.Link != nil {
			p.links = append(p.links, *entry.Link)
		}
	}
	return nil
}
