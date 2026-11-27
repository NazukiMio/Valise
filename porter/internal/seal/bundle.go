package seal

import (
	"context"
	"sort"
	"strconv"
	"time"

	"valise/core/valise"
	"valise/core/vault"
	"valise/core/vaultrpc"
	porterenroll "valise/porter/internal/enroll"
)

// SealBundle seals several instances of one harness profile as one bundle: a
// single vault lock, one shared bundle id and member list on every snapshot.
// Members of different profiles are refused before anything is written
// (bundle_cross_harness). A member failing midway leaves the earlier
// snapshots in place (the vault is append-only); the error names how many.
func SealBundle(ctx context.Context, e *porterenroll.Env, p Paths, params vaultrpc.SealBundleParams, initiator string) (*vaultrpc.SealBundleResult, error) {
	res, err := sealBundleLocked(ctx, e, p, params, initiator)
	if err == nil {
		res.AutoSync = startAutoSync(p)
	}
	return res, err
}

// SealBundlePlanned is SealBundle without the legacy targets.auto trigger; the
// accepted plan runner owns copying.
func SealBundlePlanned(ctx context.Context, e *porterenroll.Env, p Paths, params vaultrpc.SealBundleParams, initiator string) (*vaultrpc.SealBundleResult, error) {
	return sealBundleLocked(ctx, e, p, params, initiator)
}

func sealBundleLocked(ctx context.Context, e *porterenroll.Env, p Paths, params vaultrpc.SealBundleParams, initiator string) (*vaultrpc.SealBundleResult, error) {
	start := time.Now()
	kind := vault.KindSavepoint
	if initiator == vaultrpc.InitiatorTimer || initiator == vaultrpc.InitiatorAgentMCP {
		kind = vault.KindAuto
	}
	note, err := vault.NormalizeNote(params.Note)
	if err != nil {
		return nil, err
	}
	if (params.Kind != "" && params.Kind != kind) || (note != "" && (kind != vault.KindSavepoint || initiator == vaultrpc.InitiatorAgentMCP)) {
		return nil, valise.New("request_invalid", "备份类型或备注与发起方式不符", "通过定时任务建立自动快照，手动备份时可填写备注")
	}
	if _, ok := vault.TierCategories[params.Tier]; !ok {
		return nil, valise.New("tier_invalid", "未知的封存档位："+params.Tier, "可选 soul、soul-secrets、full")
	}
	members := append([]string(nil), params.Members...)
	sort.Strings(members)
	for i, m := range members {
		if i > 0 && members[i-1] == m {
			return nil, valise.New("bundle_invalid", "组合备份成员重复："+m, "每个实例只列一次")
		}
	}
	if len(members) < 2 {
		return nil, valise.New("bundle_invalid", "组合备份至少需要两个实例", "选择同一 Harness 的至少两个实例")
	}
	unlock, err := p.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	profileID := ""
	for _, m := range members {
		rec, err := porterenroll.ReadRecord(p.Home, m)
		if err != nil {
			return nil, err
		}
		if rec == nil {
			return nil, valise.New("instance_not_enrolled", "服务器上没有接入 Valise 的实例 "+m, "先接入该实例")
		}
		if rec.Superseded != nil {
			return nil, valise.New("instance_superseded", "实例 "+m+" 已被实例 "+rec.Superseded.By+" 取代", "从组合中移除该实例")
		}
		if profileID == "" {
			profileID = rec.ProfileID
		} else if rec.ProfileID != profileID {
			return nil, valise.New("bundle_cross_harness", "组合备份的成员必须属于同一个 Harness", "分别备份不同 Harness 的实例")
		}
	}
	id := vault.NewBundleID(time.Now().UTC())
	res := &vaultrpc.SealBundleResult{BundleID: id, Members: members}
	for i, m := range members {
		rep, err := sealWithLock(ctx, e, p, vaultrpc.SealParams{InstanceID: m, Tier: params.Tier, Reason: params.Reason, Kind: params.Kind, Note: params.Note},
			initiator, start, kind, note, &bundleRef{ID: id, Members: members})
		if err != nil {
			if i > 0 {
				return res, valise.New("bundle_partial", "组合备份在第 "+strconv.Itoa(i+1)+" 个成员处失败，前 "+strconv.Itoa(i)+" 个成员已保存", "单独重试失败的实例；已保存的快照不受影响").WithDetail(err.Error())
			}
			return nil, err
		}
		res.Reports = append(res.Reports, *rep)
	}
	return res, nil
}
