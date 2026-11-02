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

// Deleting whole leaves (docs/VAULT_FORMAT.md 10.4). Only the user starts
// these, through the client; nothing deletes automatically.

type deletingMark struct {
	Started   time.Time `json:"started"`
	Initiator string    `json:"initiator"`
}

// refuseCurrent rejects the leaf an instance writes to today.
func refuseCurrent(p Paths, leaf string) error {
	inst, _, ok := vault.SplitLeaf(leaf)
	if !ok {
		return valise.New("leaf_invalid", "无效的周期："+leaf, "使用 valise vault status 列出的名称")
	}
	if leaf == vault.LegacyLeaf {
		return nil
	}
	all, err := Leaves(p)
	if err != nil {
		return err
	}
	newest := ""
	for _, l := range all {
		if l.Period != nil && l.Period.InstanceID == inst {
			newest = l.Leaf
		}
	}
	if newest == leaf {
		for _, l := range all {
			if l.Leaf == leaf && l.Period != nil && !l.Deleting {
				day := now().UTC().Format("2006-01-02")
				if l.Period.TZ != "" {
					day = vault.PeriodSetting{TZ: l.Period.TZ}.LocalDay(now()).Format("2006-01-02")
				}
				if day <= l.Period.End {
					return valise.New("leaf_current", "周期 "+leaf+" 是实例 "+inst+" 当前正在写入的周期，不能删除", "等这个周期结束后再删除")
				}
			}
		}
	}
	return nil
}

// DeleteLeaf deletes one whole leaf on the server.
func DeleteLeaf(p Paths, leaf, initiator string) (res *vaultrpc.LeafDeleteResult, err error) {
	dir, err := p.LeafDir(leaf)
	if err != nil {
		return nil, err
	}
	if err := refuseCurrent(p, leaf); err != nil {
		return nil, err
	}
	if _, err := os.Stat(dir); err != nil {
		return nil, valise.New("leaf_missing", "服务器上没有周期 "+leaf, "用 valise vault status 列出现有周期")
	}
	unlock, err := p.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	defer func() {
		ent := AuditEntry{Time: time.Now().UTC(), Op: "leaf_delete", Instance: leafInstance(leaf), Initiator: initiator, Result: "ok", Reason: leaf}
		if err != nil {
			ent.Result = "failed"
		} else {
			ent.Bytes = res.Bytes
		}
		p.AppendAudit(ent)
	}()
	res = &vaultrpc.LeafDeleteResult{Leaf: leaf, Bytes: dirBytes(dir)}
	var vaultID string
	if b, err := os.ReadFile(filepath.Join(dir, vault.DescriptorFile)); err == nil {
		var d vault.Descriptor
		if json.Unmarshal(b, &d) == nil {
			vaultID = d.VaultID
		}
	}
	mark, _ := json.Marshal(deletingMark{Started: time.Now().UTC(), Initiator: initiator})
	if err := writeAtomic(filepath.Join(dir, vault.DeletingFile), append(mark, '\n'), 0o600); err != nil {
		return nil, err
	}
	for _, sub := range []string{vault.SnapshotsDir, vault.IndexDir, vault.PacksDir, vault.KeysDir, vault.EpochsDir} {
		if err := os.RemoveAll(filepath.Join(dir, sub)); err != nil {
			return nil, err
		}
	}
	for _, f := range []string{vault.DescriptorFile, vault.PeriodFile} {
		if err := os.Remove(filepath.Join(dir, f)); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	if err := os.RemoveAll(dir); err != nil {
		return nil, err
	}
	if vaultID != "" && vault.ValidID(vaultID) {
		os.Remove(p.DedupeKey(vaultID))
	}
	return res, nil
}

func leafInstance(leaf string) string {
	inst, _, _ := vault.SplitLeaf(leaf)
	return inst
}

// remoteBase returns where a leaf lives on a target: tree leaves under
// <dest>/<server_name>/, the legacy vault at <dest>/<vault_id>/.
func remoteBases(p Paths, t *vaultrpc.Target) (tree, legacy string, err error) {
	name, err := ReadServerName(p)
	if err != nil {
		return "", "", err
	}
	if name != "" {
		tree = treeDest(t, name)
	}
	if b, err := os.ReadFile(filepath.Join(p.LegacyVault(), vault.DescriptorFile)); err == nil {
		var d vault.Descriptor
		if json.Unmarshal(b, &d) == nil && vault.ValidID(d.VaultID) {
			legacy = strings.TrimRight(t.Dest, "/") + "/" + d.VaultID
		}
	}
	return tree, legacy, nil
}

// TargetList lists what a target holds for this server (rsync --list-only;
// only names and sizes are read).
func TargetList(ctx context.Context, e *porterenroll.Env, p Paths, name string) (*vaultrpc.TargetListResult, error) {
	t, err := findTarget(p, name)
	if err != nil {
		return nil, err
	}
	tree, legacy, err := remoteBases(p, t)
	if err != nil {
		return nil, err
	}
	res := &vaultrpc.TargetListResult{Target: t.Name, Dest: tree, Leaves: []vaultrpc.RemoteLeaf{}}
	byLeaf := map[string]*vaultrpc.RemoteLeaf{}
	var order []string
	add := func(leaf, rel string, size int64) {
		l := byLeaf[leaf]
		if l == nil {
			l = &vaultrpc.RemoteLeaf{Leaf: leaf, Snapshots: []string{}, Packs: []string{}}
			byLeaf[leaf] = l
			order = append(order, leaf)
		}
		l.Bytes += size
		switch {
		case rel == vault.DeletingFile:
			l.Deleting = true
		case strings.HasPrefix(rel, vault.SnapshotsDir+"/") && strings.HasSuffix(rel, ".meta.json"):
			l.Snapshots = append(l.Snapshots, strings.TrimSuffix(strings.TrimPrefix(rel, vault.SnapshotsDir+"/"), ".meta.json"))
		case strings.HasPrefix(rel, vault.PacksDir+"/"):
			l.Packs = append(l.Packs, strings.TrimSuffix(filepath.Base(rel), ".pack"))
		}
	}
	if tree != "" {
		entries, err := listRemote(ctx, e, p, t, tree)
		if err != nil {
			return nil, err
		}
		for _, en := range entries {
			parts := strings.SplitN(en.Path, "/", 3)
			if len(parts) < 3 {
				continue
			}
			leaf := parts[0] + "/" + parts[1]
			if _, _, ok := vault.SplitLeaf(leaf); !ok || leaf == vault.LegacyLeaf {
				continue
			}
			add(leaf, parts[2], en.Size)
		}
	}
	if legacy != "" {
		entries, err := listRemote(ctx, e, p, t, legacy)
		if err != nil {
			return nil, err
		}
		for _, en := range entries {
			add(vault.LegacyLeaf, en.Path, en.Size)
		}
	}
	for _, leaf := range order {
		res.Leaves = append(res.Leaves, *byLeaf[leaf])
	}
	return res, nil
}

// listRemote lists a target's files under base, falling back to sftp.
func listRemote(ctx context.Context, e *porterenroll.Env, p Paths, t *vaultrpc.Target, base string) (entries []stationsync.Entry, err error) {
	_, _, err = withRemote(ctx, e, p, t, func(r stationsync.Remote) error {
		all, err := r.List(ctx, base)
		entries = entries[:0]
		for _, en := range all {
			if !en.Dir {
				entries = append(entries, en)
			}
		}
		return err
	})
	return entries, err
}

// TargetLeafDelete deletes one leaf on a target: a DELETING marker first,
// then its files, the marker last (remote.go).
func TargetLeafDelete(ctx context.Context, e *porterenroll.Env, p Paths, name, leaf, initiator string) (err error) {
	t, err := findTarget(p, name)
	if err != nil {
		return err
	}
	inst, period, ok := vault.SplitLeaf(leaf)
	if !ok {
		return valise.New("leaf_invalid", "无效的周期："+leaf, "使用云备份列表中的名称")
	}
	if err := refuseCurrent(p, leaf); err != nil {
		return err
	}
	tree, legacy, err := remoteBases(p, t)
	if err != nil {
		return err
	}
	parent, dirName := tree+"/"+inst, period
	if leaf == vault.LegacyLeaf {
		if legacy == "" {
			return valise.New("leaf_missing", "无法确定旧金库在 "+t.Name+" 上的位置", "服务器上的旧金库已经不存在")
		}
		parent, dirName = strings.TrimRight(t.Dest, "/"), filepath.Base(legacy)
	} else if tree == "" {
		return valise.New("server_name_missing", "这台服务器还没有名字", "先给服务器起名：valise server name <服务器> <名字>")
	}
	unlock, err := p.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	defer func() {
		ent := AuditEntry{Time: time.Now().UTC(), Op: "target_leaf_delete", Instance: inst, Initiator: initiator, Target: t.Name, Result: "ok", Reason: leaf}
		if err != nil {
			ent.Result = "failed"
		}
		p.AppendAudit(ent)
	}()
	_, _, err = withRemote(ctx, e, p, t, func(r stationsync.Remote) error { return r.DeleteDir(ctx, parent, dirName) })
	if err != nil {
		return err
	}
	forgetSynced(p, t.Name, leaf)
	return nil
}

// forgetSynced drops a deleted leaf's escape pods from the target's sync
// record, so they no longer count as backed up there.
func forgetSynced(p Paths, target, leaf string) {
	st, err := Status(p)
	if err != nil {
		return
	}
	gone := map[string]bool{}
	for _, l := range st.Leaves {
		if l.Leaf == leaf {
			for _, id := range l.Snapshots {
				gone[id] = true
			}
		}
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

// RecordSynced adds escape pods the client relayed to a target to its sync
// record (docs/ARCHITECTURE.md section 7); only ids this server holds count.
func RecordSynced(p Paths, target string, ids []string) error {
	t, err := findTarget(p, target)
	if err != nil {
		return err
	}
	st, err := Status(p)
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, m := range st.Snapshots {
		known[m.SnapshotID] = true
	}
	state := vaultrpc.TargetState{Target: *t}
	if b, err := os.ReadFile(p.SyncState(target)); err == nil {
		json.Unmarshal(b, &state)
		state.Target = *t
	}
	have := map[string]bool{}
	for _, id := range state.LastSynced {
		have[id] = true
	}
	for _, id := range ids {
		if known[id] && !have[id] {
			state.LastSynced = append(state.LastSynced, id)
			have[id] = true
		}
	}
	now := time.Now().UTC()
	state.LastOK, state.LastError = &now, ""
	out, _ := json.MarshalIndent(state, "", "  ")
	p.AppendAudit(AuditEntry{Time: now, Op: "sync", Initiator: vaultrpc.InitiatorClient, Target: target, Result: "ok", Reason: "relayed by the client"})
	return writeAtomic(p.SyncState(target), out, 0o600)
}
