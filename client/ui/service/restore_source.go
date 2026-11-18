package service

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"valise/core/revive"

	"valise/core/journey"
	"valise/core/valise"
	"valise/core/vault"
)

// RestoreSource binds a selected journey copy to one existing reader. It is
// descriptive, never an authority to read arbitrary remote paths.
type RestoreSource struct {
	Copy journey.Copy
	Spec string
}

func (s *Service) RestoreSources(p journey.TimelinePoint) []RestoreSource {
	if p.MetadataState == "conflict" {
		return nil
	}
	var out []RestoreSource
	for _, cp := range p.Copies {
		server, _, ok := strings.Cut(cp.SourceLocator, "/")
		if !ok || !validServerFolder(server) {
			continue
		}
		spec := ""
		switch cp.LocationRef.Kind {
		case "server_vault":
			if cp.LocationRef.ID == "" {
				continue
			}
		case "local_repo":
			found := false
			for _, r := range s.Repos() {
				found = found || r.Name == cp.LocationRef.ID
			}
			if !found {
				continue
			}
			spec = "repo:" + cp.LocationRef.ID + "/" + server
		case "cloud":
			if cp.LocationRef.AuthorityID != "local" || !s.IsCloudHere(cp.LocationRef.ID) {
				continue
			}
			spec = "cloud:" + cp.LocationRef.ID + "/" + server
		default:
			continue
		}
		out = append(out, RestoreSource{Copy: cp, Spec: spec})
	}
	return out
}

type RestoreRequest struct {
	Options  ReviveOptions
	Snapshot journey.SnapshotRef
	Source   RestoreSource
}

// PreparedRestore owns the decrypted plan only in this human process.
type PreparedRestore struct {
	Revive       *RevivePlan
	Request      RestoreRequest
	Expires      time.Time
	stamp        string
	mu           sync.Mutex
	stageExpires time.Time
}

func restoreError(code, reason string) error {
	return valise.New(code, reason, "重新选择来源与目标并查看恢复计划；不要重复发送交换请求")
}

func (s *Service) PrepareRestore(ctx context.Context, req RestoreRequest) (*PreparedRestore, error) {
	stopOp := s.BeginOperation()
	defer stopOp()
	o := req.Options
	if req.Snapshot.VaultID == "" || req.Snapshot.SnapshotID != o.SnapshotID || req.Source.Copy.HeaderDigest == "" {
		return nil, restoreError("source_invalid", "恢复必须绑定一个完整时间点和所选副本")
	}
	p := journey.TimelinePoint{SnapshotRef: req.Snapshot, Copies: []journey.Copy{req.Source.Copy}}
	allowed := false
	for _, source := range s.RestoreSources(p) {
		allowed = allowed || source.Spec == req.Source.Spec
	}
	if !allowed {
		return nil, restoreError("source_invalid", "这个位置尚未接通恢复读取，请先选已支持的位置")
	}
	if req.Source.Spec == "" && o.From != req.Source.Copy.LocationRef.ID {
		return nil, restoreError("source_invalid", "所选服务器备份库与读取服务器不一致")
	}
	if o.Mode == revive.ModeRollback {
		if err := s.currentRestoreRoots(ctx, &o, req.Source.Copy.Metadata.InstanceID); err != nil {
			return nil, err
		}
	}
	o.Source = req.Source.Spec
	rp, err := s.PlanRevive(ctx, o, "")
	if err != nil {
		return nil, err
	}
	if err := currentRestorePlan(rp, req.Source.Copy.Metadata.InstanceID, o.Roots); err != nil {
		rp.Close()
		return nil, err
	}
	head, err := os.ReadFile(filepath.Join(rp.vault.Dir, vault.SnapshotPath(o.SnapshotID)))
	if err != nil || rp.vault.Desc.VaultID != req.Snapshot.VaultID || vault.SHA256Hex(head) != req.Source.Copy.HeaderDigest {
		rp.Close()
		return nil, restoreError("source_conflict", "读取到的备份与选定时间点不一致")
	}
	if !slices.Contains(rp.targetCaps, revive.ReviewCapability) {
		rp.block("unsupported_capability", "目标搬运工尚不支持恢复任务核实", "先在服务器设置里更新 porter，再查看恢复计划")
	}
	if rp.Plan.Mode == revive.ModeMigrate && rp.StopSource != "" && !rp.sourceStatus {
		rp.block("source_isolation_unsupported", "原服务器搬运工无法核实停服结果", "先更新原服务器 porter 再准备迁移")
	}
	if rp.SourceLost {
		rp.Close()
		return nil, restoreError("source_lost", "原服务器不可达；本段暂不提供失联恢复执行")
	}
	req.Options = o
	prepared := &PreparedRestore{Revive: rp, Request: req, Expires: time.Now().Add(10 * time.Minute)}
	prepared.stamp, err = s.restoreStamp(ctx, prepared, "")
	if err != nil {
		rp.Close()
		return nil, err
	}
	return prepared, nil
}
