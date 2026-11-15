package service

import (
	"context"
	"os"
	"path/filepath"
	"slices"

	"valise/core/journey"
	"valise/core/valise"
	"valise/core/vault"
)

// SelectedRestoreTest freezes the human's exact point and copy. The legacy CLI
// method may still select latest; desktop actions always use this binding.
type SelectedRestoreTest struct {
	Depth    string
	Target   string
	Instance string
	Snapshot journey.SnapshotRef
	Source   RestoreSource
}

func testSelectionError() error {
	return valise.New("restore_test_source_changed", "所选备份或读取位置已变化，无法按这份选择测试", "重新读取旅程，选择具体时间点和可读取的副本后再测试")
}

func (s *Service) RunSelectedRestoreTest(ctx context.Context, req SelectedRestoreTest) (*RestoreTest, error) {
	if req.Depth != "" && req.Depth != "files" {
		return nil, testSelectionError()
	}
	cp := req.Source.Copy
	if !vault.ValidID(req.Snapshot.VaultID) || !vault.ValidID(req.Snapshot.SnapshotID) || cp.HeaderDigest == "" ||
		cp.MetadataState == "conflict" || cp.Metadata.SnapshotID != req.Snapshot.SnapshotID || cp.Metadata.InstanceID != req.Instance {
		return nil, testSelectionError()
	}
	p := journey.TimelinePoint{SnapshotRef: req.Snapshot, Copies: []journey.Copy{cp}}
	if !slices.ContainsFunc(s.RestoreSources(p), func(source RestoreSource) bool {
		return source.Spec == req.Source.Spec && source.Copy.LocationRef == cp.LocationRef
	}) {
		return nil, testSelectionError()
	}
	return s.runRestoreTest(ctx, req.Target, req.Instance, req.Snapshot.SnapshotID, &req)
}

func (req SelectedRestoreTest) verify(p *RevivePlan) error {
	h := p.snapshotHeader
	cp := req.Source.Copy.Metadata
	if h == nil || !h.CreatedAt.Equal(cp.CreatedAt) || h.Tier != cp.Tier || h.Profile.ID != cp.ProfileID || h.Epoch != cp.Epoch {
		return testSelectionError()
	}
	if p.vault.Desc.VaultID != req.Snapshot.VaultID || p.Snapshot.SnapshotID != req.Snapshot.SnapshotID || p.Snapshot.InstanceID != req.Instance {
		return testSelectionError()
	}
	bytes, err := os.ReadFile(filepath.Join(p.vault.Dir, vault.SnapshotPath(req.Snapshot.SnapshotID)))
	if err != nil || vault.SHA256Hex(bytes) != req.Source.Copy.HeaderDigest {
		return testSelectionError()
	}
	return nil
}
