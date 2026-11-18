package seal

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"valise/core/enroll"
	"valise/core/identity"
	"valise/core/valise"
	"valise/core/vault"
	"valise/core/vaultrpc"
)

func PromoteSnapshot(ctx context.Context, p Paths, params vaultrpc.SnapshotPromoteParams, initiator string) (res *vaultrpc.SnapshotPromoteResult, err error) {
	if enroll.ValidInstanceID(params.InstanceID) != nil || !strings.HasPrefix(params.SnapshotID, "s_") || !vault.ValidID(params.SnapshotID) {
		return nil, valise.New("request_invalid", "存档点来源无效", "在旅程里重新选择自动快照")
	}
	note, err := vault.NormalizeNote(params.Note)
	if err != nil {
		return nil, err
	}
	unlock, err := p.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	dest, err := savepointLeafForSeal(p, params.InstanceID)
	if err != nil {
		return nil, err
	}
	if v, err := vault.Open(dest.Dir); err == nil {
		for _, m := range v.Metas {
			if m.SnapshotID == params.SnapshotID {
				if _, ok := vault.PromotionSource(m); ok && m.InstanceID == params.InstanceID {
					return &vaultrpc.SnapshotPromoteResult{SnapshotID: m.SnapshotID, Leaf: dest.Leaf}, nil
				}
				return nil, valise.New("snapshot_not_automatic", "这个时间点已经是手动存档点", "选择一个自动快照")
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	all, err := Leaves(p)
	if err != nil {
		return nil, err
	}
	for _, leaf := range all {
		if leaf.Deleting || leaf.Period == nil || leaf.Period.InstanceID != params.InstanceID {
			continue
		}
		if _, err := os.Lstat(filepath.Join(leaf.Dir, filepath.FromSlash(vault.MetaPath(params.SnapshotID)))); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return nil, err
		}
		source, err := vault.Open(leaf.Dir)
		if err != nil {
			return nil, err
		}
		for _, m := range source.Metas {
			if m.SnapshotID != params.SnapshotID || m.InstanceID != params.InstanceID || m.Kind != vault.KindAuto {
				continue
			}
			promoted, err := vault.Promote(ctx, source, dest.Dir, m, note)
			audit := AuditEntry{Time: time.Now().UTC(), Op: "snapshot_promote", Instance: params.InstanceID,
				SnapshotID: m.SnapshotID, Initiator: initiator, Result: "failed"}
			if err != nil {
				p.AppendAudit(audit)
				return nil, valise.New("snapshot_promotion_failed", "自动快照未能保存为手动存档点", "核对源备份和服务器磁盘空间后重试").WithDetail(err.Error())
			}
			audit.Result, audit.Bytes = "ok", promoted.AddedBytes
			p.AppendAudit(audit)
			return &vaultrpc.SnapshotPromoteResult{SnapshotID: m.SnapshotID, Leaf: dest.Leaf, AddedBytes: promoted.AddedBytes}, nil
		}
	}
	return nil, valise.New("snapshot_not_automatic", "服务器上没有对应的自动快照", "刷新旅程后选择仍在源服务器上的自动快照")
}

// OriginalSnapshot maps an exact published promotion back to the reference
// in the immutable backup run, including after retirement of the source.
func OriginalSnapshot(p Paths, ref identity.SnapshotRef) (identity.SnapshotRef, error) {
	if !vault.ValidID(ref.SnapshotID) || !strings.HasPrefix(ref.SnapshotID, "s_") {
		return ref, nil
	}
	leaves, err := Leaves(p)
	if err != nil {
		return ref, err
	}
	for _, l := range leaves {
		if !vault.IsSavepointLeaf(l.Leaf) || l.Deleting {
			continue
		}
		var desc vault.Descriptor
		b, err := os.ReadFile(filepath.Join(l.Dir, vault.DescriptorFile))
		if err != nil || json.Unmarshal(b, &desc) != nil || desc.Format != vault.FormatVault || desc.VaultID != ref.VaultID {
			continue
		}
		b, err = os.ReadFile(filepath.Join(l.Dir, vault.MetaPath(ref.SnapshotID)))
		if errors.Is(err, os.ErrNotExist) {
			return ref, nil
		}
		if err != nil {
			return ref, err
		}
		var meta vault.Meta
		inst, _, _ := vault.SplitLeaf(l.Leaf)
		if json.Unmarshal(b, &meta) == nil && meta.Format == vault.FormatMeta && meta.SnapshotID == ref.SnapshotID && meta.InstanceID == inst {
			if original, ok := vault.PromotionSource(meta); ok {
				ref.VaultID = original
			}
		}
		return ref, nil
	}
	return ref, nil
}
