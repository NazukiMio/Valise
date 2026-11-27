package seal

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"valise/core/stationsync"
	"valise/core/valise"
	"valise/core/vault"
	"valise/core/vaultrpc"
	porterenroll "valise/porter/internal/enroll"
)

// Deletion of chosen snapshots inside a leaf (snapshot-delete/1). Packs are
// immutable: only packs no remaining index references are removed, and an
// index stays while an older savepoint without a full dependency table may
// need it (vault.PlanDelete).

func splitDeleteLeaf(leaf string, ids []string) (inst, period string, err error) {
	inst, period, ok := vault.SplitLeaf(leaf)
	if !ok || leaf == vault.LegacyLeaf {
		return "", "", valise.New("leaf_invalid", "无效的周期："+leaf, "使用备份位置列表中的名称")
	}
	if len(ids) == 0 {
		return "", "", valise.New("request_invalid", "没有选择要删除的备份", "选择至少一个存档点或快照")
	}
	for _, id := range ids {
		if !vault.ValidID(id) || !strings.HasPrefix(id, "s_") {
			return "", "", valise.New("request_invalid", "无效的快照编号", "使用备份列表中的编号")
		}
	}
	return inst, period, nil
}

// DeleteSnapshots deletes chosen snapshots from this server's vault.
func DeleteSnapshots(p Paths, params vaultrpc.SnapshotDeleteParams, initiator string) (res *vaultrpc.SnapshotDeleteResult, err error) {
	inst, period, err := splitDeleteLeaf(params.Leaf, params.SnapshotIDs)
	if err != nil {
		return nil, err
	}
	unlock, err := p.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	defer func() {
		ent := AuditEntry{Time: time.Now().UTC(), Op: "snapshot_delete", Instance: inst, Initiator: initiator, Result: "ok", Reason: params.Leaf}
		if err != nil {
			ent.Result = "failed"
		} else {
			ent.Bytes = res.FreedBytes
		}
		p.AppendAudit(ent)
	}()
	rep, err := vault.DeleteSnapshots(p.Vaults(), inst, period, params.SnapshotIDs)
	if err != nil {
		return nil, valise.New("snapshot_delete_failed", "无法删除所选备份", "确认周期和备份仍然存在后重试").WithDetail(err.Error())
	}
	return &vaultrpc.SnapshotDeleteResult{Leaf: params.Leaf, Deleted: rep.Deleted, Missing: rep.Missing, PacksRemoved: rep.PacksRemoved,
		FreedBytes: rep.FreedBytes, IndexesKept: rep.IndexesKept}, nil
}

// TargetSnapshotDelete deletes chosen snapshots of one leaf on a copy
// location: it reads the leaf's metas and indexes into a private temporary
// directory, plans there, then deletes in order (snapshots, indexes, packs).
func TargetSnapshotDelete(ctx context.Context, e *porterenroll.Env, p Paths, params vaultrpc.TargetSnapshotDeleteParams, initiator string) (res *vaultrpc.SnapshotDeleteResult, err error) {
	inst, period, err := splitDeleteLeaf(params.Leaf, params.SnapshotIDs)
	if err != nil {
		return nil, err
	}
	t, err := findTarget(p, params.Target)
	if err != nil {
		return nil, err
	}
	tree, _, err := remoteBases(p, t)
	if err != nil {
		return nil, err
	}
	if tree == "" {
		return nil, valise.New("server_name_missing", "这台服务器还没有名字", "先给服务器起名：valise server name <服务器> <名字>")
	}
	base := tree + "/" + inst + "/" + period
	unlock, err := p.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	defer func() {
		ent := AuditEntry{Time: time.Now().UTC(), Op: "target_snapshot_delete", Instance: inst, Initiator: initiator, Target: t.Name, Result: "ok", Reason: params.Leaf}
		if err != nil {
			ent.Result = "failed"
		} else {
			ent.Bytes = res.FreedBytes
		}
		p.AppendAudit(ent)
	}()
	if err := os.MkdirAll(p.Tmp(), 0o700); err != nil {
		return nil, err
	}
	mirror, err := os.MkdirTemp(p.Tmp(), "snapdel-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(mirror)
	_, _, err = withRemote(ctx, e, p, t, func(r stationsync.Remote) error {
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
			return valise.New("snapshot_delete_failed", "无法删除 "+t.Name+" 上所选备份", "确认周期和备份仍然存在后重试").WithDetail("vault: leaf does not exist")
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
		plan, err := vault.PlanDelete(mirror, packs, params.SnapshotIDs)
		if err != nil {
			return valise.New("snapshot_delete_failed", "无法删除 "+t.Name+" 上所选备份", "确认周期和备份仍然存在后重试").WithDetail(err.Error())
		}
		listed := map[string]int64{}
		for _, en := range entries {
			listed[en.Path] = en.Size
		}
		res = &vaultrpc.SnapshotDeleteResult{Leaf: params.Leaf, Missing: plan.Missing, IndexesKept: plan.IndexesKept}
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
		for _, id := range params.SnapshotIDs {
			if _, ok := listed[vault.MetaPath(id)]; ok {
				res.Deleted++
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	forgetSnapshotsSynced(p, t.Name, params.SnapshotIDs)
	return res, nil
}

// ForgetSnapshotsSynced drops individually deleted snapshots from a target's
// sync record, so they no longer count as backed up there.
func ForgetSnapshotsSynced(p Paths, target string, ids []string) error {
	if _, err := findTarget(p, target); err != nil {
		return err
	}
	for _, id := range ids {
		if !vault.ValidID(id) || !strings.HasPrefix(id, "s_") {
			return valise.New("request_invalid", "无效的快照编号", "")
		}
	}
	forgetSnapshotsSynced(p, target, ids)
	return nil
}

func forgetSnapshotsSynced(p Paths, target string, ids []string) {
	gone := map[string]bool{}
	for _, id := range ids {
		gone[id] = true
	}
	b, err := os.ReadFile(p.SyncState(target))
	if err != nil {
		return
	}
	var state vaultrpc.TargetState
	if json.Unmarshal(b, &state) != nil {
		return
	}
	kept := state.LastSynced[:0]
	for _, id := range state.LastSynced {
		if !gone[id] {
			kept = append(kept, id)
		}
	}
	state.LastSynced = kept
	out, _ := json.MarshalIndent(state, "", "  ")
	writeAtomic(p.SyncState(target), out, 0o600)
}
