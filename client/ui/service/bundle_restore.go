package service

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"valise/core/profile"
	"valise/core/revive"
	"valise/core/valise"
	"valise/core/vault"
	"valise/core/vaultrpc"
)

// BundleNotStandaloneError is returned by PlanRevive when a snapshot belongs
// to a multi-member bundle and AllowBundleMember is false. It unwraps to a
// *valise.UserError with Code "bundle_not_standalone".
type BundleNotStandaloneError struct {
	UserErr  *valise.UserError
	BundleID string   `json:"bundle_id,omitempty"`
	Members  []string `json:"members"`
	Others   []string `json:"others,omitempty"`
}

func (e *BundleNotStandaloneError) Error() string { return e.UserErr.Error() }
func (e *BundleNotStandaloneError) Unwrap() error { return e.UserErr }

func newBundleNotStandaloneError(bundleID, self string, members []string) error {
	others := make([]string, 0, len(members))
	for _, m := range members {
		if m != self {
			others = append(others, m)
		}
	}
	shown := strings.Join(others, "、")
	if shown == "" {
		shown = strings.Join(members, "、")
	}
	ue := valise.New(
		"bundle_not_standalone",
		fmt.Sprintf("此备份也属于 %s，无法独立恢复", shown),
		"请使用组合恢复将该 Harness 的全部成员一起恢复",
	).WithDetail("bundle_id=" + bundleID + ";members=" + strings.Join(members, ","))
	return &BundleNotStandaloneError{
		UserErr:  ue,
		BundleID: bundleID,
		Members:  append([]string(nil), members...),
		Others:   others,
	}
}

// BundleRestoreMember identifies one member snapshot to restore in a bundle.
type BundleRestoreMember struct {
	InstanceID  string `json:"instance_id"`
	SnapshotID  string `json:"snapshot_id"`
	Name        string `json:"name,omitempty"`
	SkipService bool   `json:"skip_service,omitempty"`
}

// BundleRestoreOptions configures a same-harness bundle restore.
type BundleRestoreOptions struct {
	Target               string                       `json:"target"`
	From                 string                       `json:"from,omitempty"`
	Mode                 string                       `json:"mode"` // rollback | migrate | clone
	BundleID             string                       `json:"bundle_id,omitempty"`
	Members              []BundleRestoreMember        `json:"members,omitempty"`
	RootsByInstance      map[string]map[string]string `json:"roots_by_instance,omitempty"`
	Source               string                       `json:"source,omitempty"`
	AllowDraft           bool                         `json:"allow_draft_profile,omitempty"`
	AllowSnapshotProfile bool                         `json:"allow_snapshot_profile,omitempty"`
	AllowVersionMismatch bool                         `json:"allow_version_mismatch,omitempty"`
	SkipService          bool                         `json:"skip_service,omitempty"`
}

// BundleRestorePlan holds the ordered member plans of a bundle restore: main
// agent (Name == "") first, sub-agents second.
type BundleRestorePlan struct {
	BundleID string        `json:"bundle_id,omitempty"`
	Target   string        `json:"target"`
	From     string        `json:"from"`
	Mode     string        `json:"mode"`
	Plans    []*RevivePlan `json:"plans"`
	Order    []string      `json:"order"`
}

// Close releases every member plan.
func (bp *BundleRestorePlan) Close() {
	if bp == nil {
		return
	}
	for _, p := range bp.Plans {
		if p != nil {
			p.Close()
		}
	}
}

// BundleRestoreMemberResult is one member's outcome during RunBundleRestore.
type BundleRestoreMemberResult struct {
	InstanceID string             `json:"instance_id"`
	SnapshotID string             `json:"snapshot_id"`
	Result     *revive.SwapResult `json:"result,omitempty"`
	Error      string             `json:"error,omitempty"`
	ErrorCode  string             `json:"error_code,omitempty"`
}

// BundleRestoreResult summarizes a RunBundleRestore execution.
type BundleRestoreResult struct {
	BundleID string                      `json:"bundle_id,omitempty"`
	Members  []BundleRestoreMemberResult `json:"members"`
	Partial  bool                        `json:"partial,omitempty"`
}

// orderBundleMembers sorts members so the main agent (Name == "") comes first,
// followed by sub-agents sorted by InstanceID.
func orderBundleMembers(members []BundleRestoreMember) []BundleRestoreMember {
	out := append([]BundleRestoreMember(nil), members...)
	sort.SliceStable(out, func(i, j int) bool {
		mi := out[i].Name == ""
		mj := out[j].Name == ""
		if mi != mj {
			return mi
		}
		return out[i].InstanceID < out[j].InstanceID
	})
	return out
}

// PrepareBundleRestore plans restoration for all members of a same-harness
// bundle. Main agent (Name == "") is ordered first; sub-agents follow with
// SkipService enabled when sharing the harness service or having no own unit.
func (s *Service) PrepareBundleRestore(ctx context.Context, o BundleRestoreOptions, passphrase string) (*BundleRestorePlan, error) {
	from := o.From
	if from == "" {
		from = o.Target
	}
	members := append([]BundleRestoreMember(nil), o.Members...)
	if o.BundleID != "" {
		var st *vaultrpc.StatusResult
		var err error
		if o.Source != "" {
			src, perr := s.ParseCopySource(from, o.Source)
			if perr != nil {
				return nil, perr
			}
			st, err = s.SourceStatus(ctx, src)
		} else {
			st, err = s.VaultStatus(ctx, from)
		}
		if err != nil {
			return nil, err
		}
		byInst := make(map[string]int, len(members))
		for i, mem := range members {
			byInst[mem.InstanceID] = i
		}
		for _, m := range st.Snapshots {
			if m.BundleID != o.BundleID {
				continue
			}
			if idx, ok := byInst[m.InstanceID]; ok {
				if members[idx].SnapshotID == "" {
					members[idx].SnapshotID = m.SnapshotID
				}
			} else {
				byInst[m.InstanceID] = len(members)
				members = append(members, BundleRestoreMember{
					InstanceID: m.InstanceID,
					SnapshotID: m.SnapshotID,
				})
			}
		}
	}
	if len(members) == 0 {
		return nil, valise.New("bundle_empty", "组合备份中没有可恢复的成员快照", "确认组合备份完整且仍存在")
	}
	// Best-effort enrich member Name from source instances so main agent
	// (Name == "") is planned first even when caller passed only BundleID.
	if from != "" {
		needName := false
		for _, mem := range members {
			if mem.Name == "" {
				needName = true
				break
			}
		}
		if needName {
			if insts, err := s.Instances(ctx, from); err == nil {
				nameByID := make(map[string]string, len(insts))
				for _, in := range insts {
					nameByID[in.InstanceID] = in.Name
				}
				for i := range members {
					if members[i].Name == "" {
						members[i].Name = nameByID[members[i].InstanceID]
					}
				}
			}
		}
	}
	ordered := orderBundleMembers(members)
	bp := &BundleRestorePlan{
		BundleID: o.BundleID,
		Target:   o.Target,
		From:     from,
		Mode:     o.Mode,
		Plans:    make([]*RevivePlan, 0, len(ordered)),
		Order:    make([]string, 0, len(ordered)),
	}

	occupied := map[string]string{}
	allocated := map[string]string{}
	if o.Mode == revive.ModeMigrate && o.Target != "" {
		if targetInsts, err := s.Instances(ctx, o.Target); err == nil {
			for _, in := range targetInsts {
				if in.Superseded != nil || in.StateMissing() {
					continue
				}
				for _, p := range in.Roots {
					if strings.TrimSpace(p) != "" {
						clean := filepath.Clean(p)
						if _, exists := occupied[clean]; !exists {
							occupied[clean] = in.InstanceID
						}
					}
				}
			}
		}
		for id, rmap := range o.RootsByInstance {
			if st := strings.TrimSpace(rmap[profile.StateRoot]); st != "" {
				allocated[filepath.Clean(st)] = id
			}
		}
	}
	var mainOldState, mainNewState string
	autoNamed := func(prof profile.Profile, head *vault.Snapshot, first revive.InspectResult) map[string]string {
		out := map[string]string{}
		taken := make(map[string]string, len(occupied)+len(allocated))
		for p, id := range occupied {
			taken[p] = id
		}
		for p, id := range allocated {
			taken[p] = id
		}
		oldSub := strings.TrimSpace(head.Roots[profile.StateRoot])
		var chosen string
		if mainOldState != "" && mainNewState != "" && oldSub != "" {
			oldSubClean := filepath.Clean(oldSub)
			oldMainClean := filepath.Clean(mainOldState)
			if strings.HasPrefix(oldSubClean, oldMainClean+"/") {
				cand := filepath.Clean(strings.TrimRight(mainNewState, "/") + strings.TrimPrefix(oldSubClean, oldMainClean))
				if _, used := taken[cand]; !used {
					chosen = cand
				}
			}
		}
		if chosen == "" {
			defaultDir := resolveDefaultStateDir(&prof, first.Home, prof.ID)
			req := TargetPlacementRequest{
				From:               from,
				Target:             o.Target,
				Mode:               o.Mode,
				ProfileID:          prof.ID,
				RecordedInstanceID: head.InstanceID,
			}
			chosen = computeRecommendedStateDir(&prof, first.Home, defaultDir, req, head.Name, taken, false)
		}
		if chosen != "" {
			out[profile.StateRoot] = chosen
			allocated[filepath.Clean(chosen)] = head.InstanceID
		}
		if mainOldState != "" && mainNewState != "" {
			oldMainClean := filepath.Clean(mainOldState)
			for id, old := range head.Roots {
				if id == profile.StateRoot || strings.TrimSpace(old) == "" {
					continue
				}
				oldClean := filepath.Clean(old)
				if strings.HasPrefix(oldClean, oldMainClean+"/") {
					out[id] = filepath.Clean(strings.TrimRight(mainNewState, "/") + strings.TrimPrefix(oldClean, oldMainClean))
				}
			}
		}
		return out
	}

	for _, mem := range ordered {
		skipSvc := o.SkipService || mem.SkipService || mem.Name != ""
		ro := ReviveOptions{
			Target:               o.Target,
			From:                 from,
			SnapshotID:           mem.SnapshotID,
			Mode:                 o.Mode,
			Roots:                o.RootsByInstance[mem.InstanceID],
			Source:               o.Source,
			AllowDraft:           o.AllowDraft,
			AllowSnapshotProfile: o.AllowSnapshotProfile,
			AllowVersionMismatch: o.AllowVersionMismatch,
			SkipService:          skipSvc,
			AllowBundleMember:    true,
		}
		if o.Mode == revive.ModeMigrate {
			ro.autoNamedRoots = autoNamed
		}
		rp, err := s.PlanRevive(ctx, ro, passphrase)
		if err != nil {
			bp.Close()
			return nil, err
		}
		for _, r := range rp.Plan.Roots {
			if r.ID == profile.StateRoot && r.Path != "" {
				allocated[filepath.Clean(r.Path)] = rp.Plan.InstanceID
				if rp.profileName == "" && mainNewState == "" {
					mainNewState = r.Path
					if rp.snapshotHeader != nil {
						mainOldState = rp.snapshotHeader.Roots[profile.StateRoot]
					}
				}
			}
		}
		bp.Plans = append(bp.Plans, rp)
	}
	// Re-sort by decrypted snapshot header's Name == "" in case caller didn't
	// supply Name up front.
	sort.SliceStable(bp.Plans, func(i, j int) bool {
		mi := bp.Plans[i].profileName == ""
		mj := bp.Plans[j].profileName == ""
		if mi != mj {
			return mi
		}
		return bp.Plans[i].Plan.InstanceID < bp.Plans[j].Plan.InstanceID
	})
	// Verify every declared bundle member is covered; never silently degrade
	// an incomplete bundle into a partial or single-member restore.
	declared := map[string]bool{}
	planned := map[string]bool{}
	for _, rp := range bp.Plans {
		planned[rp.Snapshot.InstanceID] = true
		planned[rp.Plan.InstanceID] = true
		if bp.BundleID == "" && rp.Snapshot.BundleID != "" {
			bp.BundleID = rp.Snapshot.BundleID
		}
		if rp.Snapshot.BundleID != "" || len(rp.Snapshot.BundleMembers) > 1 {
			for _, id := range rp.Snapshot.BundleMembers {
				if id != "" {
					declared[id] = true
				}
			}
		}
	}
	var missing []string
	for id := range declared {
		if !planned[id] {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		bp.Close()
		return nil, valise.New(
			"bundle_incomplete",
			fmt.Sprintf("组合备份不完整，缺少成员 %s 的快照，无法执行组合恢复", strings.Join(missing, "、")),
			"确认该组合备份的所有成员快照仍然存在后再重试",
		).WithDetail("bundle_id=" + bp.BundleID + ";missing=" + strings.Join(missing, ","))
	}
	// Enforce SkipService on sub-agents when a main agent is present or the
	// sub-agent has no independent service, and never stop the source service
	// twice for sub-agents.
	hasMain := len(bp.Plans) > 0 && bp.Plans[0].profileName == ""
	for i, rp := range bp.Plans {
		bp.Order = append(bp.Order, rp.Plan.InstanceID)
		if i > 0 && (rp.profileName != "" || hasMain) {
			if rp.profileName != "" {
				rp.DataOnly = true
				rp.Plan.Unit = ""
				rp.Plan.InstallService = false
				rp.InstallArgv = nil
				rp.StopSource = ""
			}
		}
		if rp.DataOnly || rp.Plan.Unit == "" {
			rp.StopSource = ""
		}
	}
	return bp, nil
}

// RunBundleRestore executes every member plan in order (main agent first,
// sub-agents second).
func (s *Service) RunBundleRestore(ctx context.Context, bp *BundleRestorePlan, confirm ReviveConfirm) (*BundleRestoreResult, error) {
	if bp == nil || len(bp.Plans) == 0 {
		return nil, valise.New("bundle_empty", "组合恢复计划为空", "重新准备组合恢复计划")
	}
	res := &BundleRestoreResult{
		BundleID: bp.BundleID,
		Members:  make([]BundleRestoreMemberResult, 0, len(bp.Plans)),
	}
	for _, rp := range bp.Plans {
		mr := BundleRestoreMemberResult{
			InstanceID: rp.Plan.InstanceID,
			SnapshotID: rp.Snapshot.SnapshotID,
		}
		swapRes, err := s.ExecuteReviveConfirmed(ctx, rp, "", confirm)
		mr.Result = swapRes
		if err != nil {
			res.Partial = true
			mr.Error = err.Error()
			if ue, ok := valise.As(err); ok {
				mr.ErrorCode = ue.Code
				mr.Error = ue.Reason
			}
		}
		res.Members = append(res.Members, mr)
	}
	return res, nil
}
