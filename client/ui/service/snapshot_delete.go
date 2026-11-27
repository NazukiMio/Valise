package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"valise/core/stationsync"
	"valise/core/valise"
	"valise/core/vault"
	"valise/core/vaultrpc"
)

// Snapshot deletion locations (docs/SERVICE_CONTRACTS.md 4.6).
const (
	SnapshotLocationServerVault  = "server_vault"
	SnapshotLocationServerTarget = "server_target"
	SnapshotLocationStation      = "station"
	SnapshotLocationLocalRepo    = "local_repo"
	SnapshotLocationLocalCloud   = "local_cloud"
)

// SnapshotDeleteLocation names one checked backup location.
type SnapshotDeleteLocation struct {
	Kind string `json:"kind"`
	Name string `json:"name,omitempty"`
}

// SnapshotDeleteRequest deletes chosen snapshots of one leaf across the
// checked locations.
type SnapshotDeleteRequest struct {
	Host        string                   `json:"host"`
	Leaf        string                   `json:"leaf"`
	SnapshotIDs []string                 `json:"snapshot_ids"`
	Locations   []SnapshotDeleteLocation `json:"locations"`
	ServerName  string                   `json:"server_name,omitempty"`
}

// SnapshotBundleWarning reports a selected snapshot that belongs to a
// same-harness bundle backup.
type SnapshotBundleWarning struct {
	SnapshotID    string   `json:"snapshot_id"`
	BundleID      string   `json:"bundle_id,omitempty"`
	BundleMembers []string `json:"bundle_members,omitempty"`
	Note          string   `json:"note,omitempty"`
}

// SnapshotDeletePreview is shown before deleting selected snapshots so the
// user sees any bundle memberships affected.
type SnapshotDeletePreview struct {
	Host          string                   `json:"host"`
	Leaf          string                   `json:"leaf"`
	SnapshotIDs   []string                 `json:"snapshot_ids"`
	Locations     []SnapshotDeleteLocation `json:"locations"`
	BundleImpacts []SnapshotBundleWarning  `json:"bundle_impacts,omitempty"`
}

// SnapshotDeleteLocationResult is the outcome on one location.
type SnapshotDeleteLocationResult struct {
	Location     SnapshotDeleteLocation `json:"location"`
	Deleted      int                    `json:"deleted"`
	Missing      []string               `json:"missing,omitempty"`
	PacksRemoved int                    `json:"packs_removed"`
	FreedBytes   int64                  `json:"freed_bytes"`
	IndexesKept  int                    `json:"indexes_kept"`
	Error        string                 `json:"error,omitempty"`
	ErrorCode    string                 `json:"error_code,omitempty"`
}

// SnapshotDeleteBatchResult collects per-location deletion results.
type SnapshotDeleteBatchResult struct {
	Host    string                         `json:"host"`
	Leaf    string                         `json:"leaf"`
	Results []SnapshotDeleteLocationResult `json:"results"`
	Partial bool                           `json:"partial,omitempty"`
}

func validateSnapshotDeleteRequest(req SnapshotDeleteRequest) (inst, period string, err error) {
	inst, period, ok := vault.SplitLeaf(req.Leaf)
	if !ok || req.Leaf == vault.LegacyLeaf {
		return "", "", valise.New("leaf_invalid", "无效的周期："+req.Leaf, "选择有效的备份周期")
	}
	if len(req.SnapshotIDs) == 0 {
		return "", "", valise.New("request_invalid", "没有选择要删除的快照", "勾选至少一个存档点或快照")
	}
	for _, id := range req.SnapshotIDs {
		if !vault.ValidID(id) || !strings.HasPrefix(id, "s_") {
			return "", "", valise.New("request_invalid", "无效的快照编号："+id, "从备份列表中选择快照")
		}
	}
	if len(req.Locations) == 0 {
		return "", "", valise.New("request_invalid", "没有选择要删除的备份位置", "勾选至少一个备份位置")
	}
	for _, loc := range req.Locations {
		switch loc.Kind {
		case SnapshotLocationServerVault:
		case SnapshotLocationServerTarget, SnapshotLocationStation, SnapshotLocationLocalRepo, SnapshotLocationLocalCloud:
			if strings.TrimSpace(loc.Name) == "" {
				return "", "", valise.New("request_invalid", "缺少备份位置名称", "指定目标位置名称")
			}
		default:
			return "", "", valise.New("request_invalid", "未知的备份位置类型："+loc.Kind, "选择支持的备份位置")
		}
	}
	return inst, period, nil
}

// PreviewSnapshotDelete checks whether any selected snapshot is part of a
// bundle backup before deletion runs.
func (s *Service) PreviewSnapshotDelete(ctx context.Context, req SnapshotDeleteRequest) (*SnapshotDeletePreview, error) {
	inst, period, err := validateSnapshotDeleteRequest(req)
	if err != nil {
		return nil, err
	}
	preview := &SnapshotDeletePreview{
		Host:        req.Host,
		Leaf:        req.Leaf,
		SnapshotIDs: append([]string(nil), req.SnapshotIDs...),
		Locations:   append([]SnapshotDeleteLocation(nil), req.Locations...),
	}
	seen := map[string]bool{}
	addImpact := func(m vault.Meta) {
		if seen[m.SnapshotID] || (m.BundleID == "" && len(m.BundleMembers) <= 1) {
			return
		}
		seen[m.SnapshotID] = true
		preview.BundleImpacts = append(preview.BundleImpacts, SnapshotBundleWarning{
			SnapshotID:    m.SnapshotID,
			BundleID:      m.BundleID,
			BundleMembers: append([]string(nil), m.BundleMembers...),
			Note:          m.Note,
		})
	}
	serverName := req.ServerName
	if req.Host != "" {
		if st, err := s.VaultStatus(ctx, req.Host); err == nil && st != nil {
			if serverName == "" {
				serverName = st.ServerName
			}
			wanted := map[string]bool{}
			for _, id := range req.SnapshotIDs {
				wanted[id] = true
			}
			for _, m := range st.Snapshots {
				if wanted[m.SnapshotID] {
					addImpact(m)
				}
			}
		}
	}
	if serverName != "" {
		for _, loc := range req.Locations {
			if loc.Kind != SnapshotLocationLocalRepo {
				continue
			}
			r, err := s.repo(loc.Name)
			if err != nil {
				continue
			}
			if metas, err := vault.BundleOf(filepath.Join(r.Path, serverName), inst, period, req.SnapshotIDs); err == nil {
				for _, m := range metas {
					addImpact(m)
				}
			}
		}
	}
	return preview, nil
}

// DeleteSnapshots deletes the selected snapshots across every checked location.
// Each location is processed independently; failures are reported per location.
func (s *Service) DeleteSnapshots(ctx context.Context, req SnapshotDeleteRequest) (*SnapshotDeleteBatchResult, error) {
	stopOp := s.BeginOperation()
	defer stopOp()
	inst, period, err := validateSnapshotDeleteRequest(req)
	if err != nil {
		return nil, err
	}
	var status *vaultrpc.StatusResult
	serverName := req.ServerName
	needsStatus := false
	for _, loc := range req.Locations {
		if loc.Kind == SnapshotLocationStation || loc.Kind == SnapshotLocationLocalCloud ||
			(loc.Kind == SnapshotLocationLocalRepo && serverName == "") ||
			(loc.Kind == SnapshotLocationServerTarget && s.IsStation(loc.Name)) {
			needsStatus = true
			break
		}
	}
	if needsStatus && req.Host != "" {
		if st, serr := s.VaultStatus(ctx, req.Host); serr == nil {
			status = st
			if serverName == "" {
				serverName = st.ServerName
			}
		} else if serverName == "" {
			return nil, serr
		}
	}
	out := &SnapshotDeleteBatchResult{
		Host:    req.Host,
		Leaf:    req.Leaf,
		Results: make([]SnapshotDeleteLocationResult, 0, len(req.Locations)),
	}
	for _, loc := range req.Locations {
		var r SnapshotDeleteLocationResult
		r.Location = loc
		var res *vaultrpc.SnapshotDeleteResult
		var derr error
		switch loc.Kind {
		case SnapshotLocationServerVault:
			res, derr = s.serverSnapshotDelete(ctx, req.Host, req.Leaf, req.SnapshotIDs)
		case SnapshotLocationServerTarget:
			if s.IsStation(loc.Name) {
				res, derr = s.stationSnapshotDelete(ctx, req.Host, loc.Name, req.Leaf, req.SnapshotIDs, status, serverName)
			} else {
				res, derr = s.targetSnapshotDelete(ctx, req.Host, loc.Name, req.Leaf, req.SnapshotIDs)
			}
		case SnapshotLocationStation:
			res, derr = s.stationSnapshotDelete(ctx, req.Host, loc.Name, req.Leaf, req.SnapshotIDs, status, serverName)
		case SnapshotLocationLocalRepo:
			res, derr = s.repoSnapshotDelete(ctx, req.Host, loc.Name, serverName, inst, period, req.SnapshotIDs)
		case SnapshotLocationLocalCloud:
			res, derr = s.localCloudSnapshotDelete(ctx, req.Host, loc.Name, req.Leaf, req.SnapshotIDs, status, serverName)
		}
		if res != nil {
			r.Deleted = res.Deleted
			r.Missing = res.Missing
			r.PacksRemoved = res.PacksRemoved
			r.FreedBytes = res.FreedBytes
			r.IndexesKept = res.IndexesKept
		}
		if derr != nil {
			out.Partial = true
			r.Error = derr.Error()
			if ue, ok := valise.As(derr); ok {
				r.ErrorCode = ue.Code
				r.Error = ue.Reason
			}
		}
		out.Results = append(out.Results, r)
	}
	return out, nil
}

func (s *Service) serverSnapshotDelete(ctx context.Context, host, leaf string, ids []string) (*vaultrpc.SnapshotDeleteResult, error) {
	var res vaultrpc.SnapshotDeleteResult
	err := s.call(ctx, host, vaultrpc.MethodSnapshotDelete, vaultrpc.SnapshotDeleteParams{Leaf: leaf, SnapshotIDs: ids}, &res)
	if err != nil {
		return nil, err
	}
	return &res, nil
}

func (s *Service) targetSnapshotDelete(ctx context.Context, host, target, leaf string, ids []string) (*vaultrpc.SnapshotDeleteResult, error) {
	var res vaultrpc.SnapshotDeleteResult
	err := s.call(ctx, host, vaultrpc.MethodTargetSnapshotDelete, vaultrpc.TargetSnapshotDeleteParams{Target: target, Leaf: leaf, SnapshotIDs: ids}, &res)
	if err != nil {
		return nil, err
	}
	return &res, nil
}

func (s *Service) repoSnapshotDelete(ctx context.Context, host, name, serverName, inst, period string, ids []string) (*vaultrpc.SnapshotDeleteResult, error) {
	r, err := s.repo(name)
	if err != nil {
		return nil, err
	}
	if !s.repoAvailable(r.Path) {
		return nil, valise.New("repo_unavailable", "仓库 "+name+" 的文件夹现在不可用："+r.Path, "接上外接硬盘或检查路径后重试")
	}
	if serverName == "" {
		return nil, valise.New("server_name_missing", "缺少服务器名称，无法定位仓库目录", "先给服务器起名或传入服务器名称")
	}
	unlock, err := s.lockLocation(ctx, "repo", name, host, true)
	if err != nil {
		return nil, err
	}
	defer unlock()
	rep, err := vault.DeleteSnapshots(filepath.Join(r.Path, serverName), inst, period, ids)
	if err != nil {
		return nil, valise.New("snapshot_delete_failed", "无法删除仓库 "+name+" 中所选备份", "确认周期和备份仍然存在后重试").WithDetail(err.Error())
	}
	return &vaultrpc.SnapshotDeleteResult{
		Leaf:         inst + "/" + period,
		Deleted:      rep.Deleted,
		Missing:      rep.Missing,
		PacksRemoved: rep.PacksRemoved,
		FreedBytes:   rep.FreedBytes,
		IndexesKept:  rep.IndexesKept,
	}, nil
}

func (s *Service) stationSnapshotDelete(ctx context.Context, host, name, leaf string, ids []string, status *vaultrpc.StatusResult, serverName string) (*vaultrpc.SnapshotDeleteResult, error) {
	st, err := s.station(name)
	if err != nil {
		return nil, err
	}
	if serverName == "" {
		return nil, valise.New("server_name_missing", host+" 还没有名字", "先给服务器起名")
	}
	if err := s.authorizeRemoteSnapshotDelete(ctx, host, status, ids); err != nil {
		return nil, err
	}
	rs := s.relayStation(*st)
	base := rs.Dest(strings.TrimRight(st.Path, "/") + "/" + serverName + "/" + leaf)
	res, err := s.remoteSnapshotDelete(ctx, rs, base, leaf, ids)
	if err != nil {
		return nil, err
	}
	if status != nil && attached(status, *st) && host != "" {
		_ = s.call(ctx, host, vaultrpc.MethodSyncRecord, vaultrpc.SyncRecordParams{Target: name, ForgetSnapshots: ids}, nil)
	}
	return res, nil
}

func (s *Service) localCloudSnapshotDelete(ctx context.Context, host, name, leaf string, ids []string, status *vaultrpc.StatusResult, serverName string) (*vaultrpc.SnapshotDeleteResult, error) {
	if !s.IsCloudHere(name) {
		return nil, valise.New("cloud_unknown", "这台电脑没有登录云盘 "+name, "先登录：valise cloud add")
	}
	if serverName == "" {
		return nil, valise.New("server_name_missing", host+" 还没有名字", "先给服务器起名")
	}
	if err := s.authorizeRemoteSnapshotDelete(ctx, host, status, ids); err != nil {
		return nil, err
	}
	unlock, err := s.lockLocation(ctx, "cloud", name, host, true)
	if err != nil {
		return nil, err
	}
	defer unlock()
	st, err := s.hereStation(ctx, name)
	if err != nil {
		return nil, err
	}
	dest := st.Dest(s.cloudFolderOf(name) + serverName + "/" + leaf)
	res, err := s.remoteSnapshotDelete(ctx, st, dest, leaf, ids)
	if err != nil {
		return nil, err
	}
	if status != nil && host != "" {
		for _, t := range status.Targets {
			if t.Name == name && t.Kind == stationsync.MethodRclone {
				_ = s.call(ctx, host, vaultrpc.MethodSyncRecord, vaultrpc.SyncRecordParams{Target: name, ForgetSnapshots: ids}, nil)
				break
			}
		}
	}
	return res, nil
}

func (s *Service) authorizeRemoteSnapshotDelete(ctx context.Context, host string, status *vaultrpc.StatusResult, ids []string) error {
	if host == "" {
		return nil
	}
	onServer := false
	if status != nil {
		have := map[string]bool{}
		for _, m := range status.Snapshots {
			have[m.SnapshotID] = true
		}
		onServer = len(ids) > 0
		for _, id := range ids {
			if !have[id] {
				onServer = false
				break
			}
		}
	}
	if onServer {
		_, err := s.ownerIdentity(host)
		return err
	}
	_, done, err := s.adminTransport(ctx, host)
	if err != nil {
		return err
	}
	done()
	return nil
}

func (s *Service) remoteSnapshotDelete(ctx context.Context, st stationsync.Station, base, leaf string, ids []string) (*vaultrpc.SnapshotDeleteResult, error) {
	mirror, err := os.MkdirTemp(s.Dirs.Cache, "snapdel-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(mirror)
	var res *vaultrpc.SnapshotDeleteResult
	_, _, err = stationsync.With(ctx, localRunner{}, st, func(r stationsync.Remote) error {
		entries, err := r.List(ctx, base)
		if err != nil {
			return err
		}
		var fetch, packs []string
		hasDescriptor := false
		for _, en := range entries {
			switch {
			case en.Dir || !vault.ValidRel(en.Path):
			case en.Path == vault.DescriptorFile:
				hasDescriptor = true
			case strings.HasPrefix(en.Path, vault.SnapshotsDir+"/") && strings.HasSuffix(en.Path, ".meta.json"),
				strings.HasPrefix(en.Path, vault.IndexDir+"/"):
				fetch = append(fetch, en.Path)
			case strings.HasPrefix(en.Path, vault.PacksDir+"/"):
				packs = append(packs, en.Path)
			}
		}
		if !hasDescriptor {
			return valise.New("snapshot_delete_failed", "无法删除 "+st.Name+" 上所选备份", "确认周期和备份仍然存在后重试").WithDetail("vault: leaf does not exist")
		}
		for _, sub := range []string{vault.SnapshotsDir, vault.IndexDir} {
			if err := os.MkdirAll(filepath.Join(mirror, sub), 0o700); err != nil {
				return err
			}
		}
		if err := os.WriteFile(filepath.Join(mirror, vault.DescriptorFile), nil, 0o600); err != nil {
			return err
		}
		if len(fetch) > 0 {
			if err := r.Fetch(ctx, base, fetch, mirror); err != nil {
				return err
			}
		}
		plan, err := vault.PlanDelete(mirror, packs, ids)
		if err != nil {
			return valise.New("snapshot_delete_failed", "无法删除 "+st.Name+" 上所选备份", "确认周期和备份仍然存在后重试").WithDetail(err.Error())
		}
		listed := map[string]int64{}
		for _, en := range entries {
			listed[en.Path] = en.Size
		}
		res = &vaultrpc.SnapshotDeleteResult{Leaf: leaf, Missing: plan.Missing, IndexesKept: plan.IndexesKept}
		for _, group := range plan.Groups() {
			var existing []string
			for _, rel := range group {
				if size, ok := listed[rel]; ok {
					existing = append(existing, rel)
					res.FreedBytes += size
					if strings.HasPrefix(rel, vault.PacksDir+"/") {
						res.PacksRemoved++
					}
				}
			}
			if err := r.DeleteFiles(ctx, base, existing); err != nil {
				return err
			}
		}
		for _, id := range ids {
			if _, ok := listed[vault.MetaPath(id)]; ok {
				res.Deleted++
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, errors.New("empty delete result")
	}
	return res, nil
}

// containsLocation reports whether locs contains kind/name.
func containsLocation(locs []SnapshotDeleteLocation, kind, name string) bool {
	return slices.ContainsFunc(locs, func(l SnapshotDeleteLocation) bool {
		return l.Kind == kind && l.Name == name
	})
}
