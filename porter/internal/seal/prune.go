package seal

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"valise/core/stationsync"

	"valise/core/valise"
	"valise/core/vault"
	"valise/core/vaultrpc"
	porterenroll "valise/porter/internal/enroll"
)

// Whole-leaf deletion by an explicit request or an accepted retention plan.

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
	if vault.IsSavepointLeaf(leaf) {
		return valise.New("leaf_permanent", "手动存档点永久保留，不能整组删除", "只选择已结束的日期周期")
	}
	all, err := Leaves(p)
	if err != nil {
		return err
	}
	for _, l := range all {
		if l.Leaf == leaf && l.Period != nil && !periodEnded(l.Period, now()) {
			return valise.New("leaf_current", "周期 "+leaf+" 是实例 "+inst+" 当前正在写入的周期，不能删除", "等这个周期结束后再删除")
		}
	}
	return nil
}

// DeleteLeaf deletes one whole leaf on the server.
func DeleteLeaf(p Paths, leaf, initiator string) (res *vaultrpc.LeafDeleteResult, err error) {
	unlock, err := p.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := refuseCurrent(p, leaf); err != nil {
		return nil, err
	}
	return deleteLeafLocked(p, leaf, initiator, "leaf_delete")
}

func deleteLeafLocked(p Paths, leaf, initiator, op string) (res *vaultrpc.LeafDeleteResult, err error) {
	dir, err := p.LeafDir(leaf)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(dir); err != nil {
		return nil, valise.New("leaf_missing", "服务器上没有周期 "+leaf, "用 valise vault status 列出现有周期")
	}
	defer func() {
		ent := AuditEntry{Time: time.Now().UTC(), Op: op, Instance: leafInstance(leaf), Initiator: initiator, Result: "ok", Reason: leaf}
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

func periodEnded(period *vault.Period, at time.Time) bool {
	day := at.UTC()
	if period.TZ != "" {
		day = (vault.PeriodSetting{TZ: period.TZ}).LocalDay(at)
	}
	return day.Format("2006-01-02") > period.End
}

// RetireExpiredAutoLeaves implements a user-confirmed server-only retention
// plan. Old or mixed leaves with a historical snapshot are left to explicit
// review. The lock binds eligibility to deletion and serializes promotion.
func RetireExpiredAutoLeaves(p Paths, instance string, keepPeriods int) error {
	if keepPeriods <= 0 {
		return nil
	}
	unlock, err := p.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	all, err := Leaves(p)
	if err != nil {
		return err
	}
	at := now()
	var ended, resume []LeafInfo
	for _, leaf := range all {
		if leaf.Period == nil || leaf.Period.InstanceID != instance || !periodEnded(leaf.Period, at) {
			continue
		}
		if leaf.Deleting {
			var mark deletingMark
			b, err := os.ReadFile(filepath.Join(leaf.Dir, vault.DeletingFile))
			if err == nil && json.Unmarshal(b, &mark) == nil && mark.Initiator == vaultrpc.InitiatorAuto {
				resume = append(resume, leaf)
			}
			continue
		}
		setting := vault.PeriodSetting{Unit: leaf.Period.Unit, TZ: leaf.Period.TZ}
		if leaf.Period.Format == vault.PeriodFormat && setting.Validate() == nil && onlyAutomatic(leaf, instance) {
			ended = append(ended, leaf)
		}
	}
	sort.Slice(ended, func(i, j int) bool {
		if ended[i].Period.End == ended[j].Period.End {
			return ended[i].Leaf < ended[j].Leaf
		}
		return ended[i].Period.End < ended[j].Period.End
	})
	retire := append(resume, ended[:max(0, len(ended)-keepPeriods)]...)
	for _, leaf := range retire {
		if _, err := deleteLeafLocked(p, leaf.Leaf, vaultrpc.InitiatorAuto, "leaf_retire"); err != nil {
			return err
		}
	}
	return nil
}

func onlyAutomatic(leaf LeafInfo, instance string) bool {
	dir := filepath.Join(leaf.Dir, vault.SnapshotsDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	metas, headers := map[string]bool{}, map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if id, ok := strings.CutSuffix(name, ".age"); ok && entry.Type().IsRegular() {
			headers[id] = true
		}
		if id, ok := strings.CutSuffix(name, ".meta.json"); ok {
			if !entry.Type().IsRegular() {
				return false
			}
			var meta vault.Meta
			b, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil || json.Unmarshal(b, &meta) != nil ||
				!strings.HasPrefix(id, "s_") || !vault.ValidID(id) || meta.SnapshotID != id ||
				meta.Format != vault.FormatMeta || meta.Kind != vault.KindAuto || meta.InstanceID != instance {
				return false
			}
			metas[id] = true
		}
	}
	// An ended period whose snapshots were all deleted by hand holds nothing
	// to protect: it may retire like any other empty automatic period.
	if len(metas) != 0 || len(headers) != 0 {
		if len(metas) == 0 || len(metas) != len(headers) {
			return false
		}
		for id := range metas {
			if !headers[id] {
				return false
			}
		}
	}
	_, err = os.Lstat(filepath.Join(leaf.Dir, vault.DeletingFile))
	return errors.Is(err, os.ErrNotExist)
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
		return valise.New("leaf_invalid", "无效的周期："+leaf, "使用备份位置列表中的名称")
	}
	tree, legacy, err := remoteBases(p, t)
	if err != nil {
		return err
	}
	parent, dirName := tree+"/"+inst, period
	if leaf == vault.LegacyLeaf {
		if legacy == "" {
			return valise.New("leaf_missing", "无法确定旧备份库在 "+t.Name+" 上的位置", "服务器上的旧备份库已经不存在")
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
	if err := refuseCurrent(p, leaf); err != nil {
		return err
	}
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

// ForgetSynced is forgetSynced for a period the client deleted.
func ForgetSynced(p Paths, target, leaf string) error {
	if _, _, ok := vault.SplitLeaf(leaf); !ok {
		return valise.New("leaf_invalid", "无效的周期："+leaf, "")
	}
	if _, err := findTarget(p, target); err != nil {
		return err
	}
	forgetSynced(p, target, leaf)
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
