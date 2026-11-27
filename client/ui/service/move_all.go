package service

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"valise/core/enroll"
	"valise/core/protection"
	"valise/core/revive"
	"valise/core/valise"
	"valise/core/vault"
)

// HarnessMoveMember describes one active instance inside a same-harness group
// for one-click migration.
type HarnessMoveMember struct {
	InstanceID  string      `json:"instance_id"`
	Name        string      `json:"name,omitempty"`
	DisplayName string      `json:"display_name,omitempty"`
	Snapshot    *vault.Meta `json:"snapshot,omitempty"`
	Ready       bool        `json:"ready"`
	Reason      string      `json:"reason,omitempty"`
}

// HarnessMoveGroup groups all active instances of one ProfileID on the source
// server. Ready is true only when every member has a full or raw snapshot
// (standalone or same-harness bundle).
type HarnessMoveGroup struct {
	ProfileID string              `json:"profile_id"`
	Members   []HarnessMoveMember `json:"members"`
	BundleID  string              `json:"bundle_id,omitempty"`
	Ready     bool                `json:"ready"`
	Reason    string              `json:"reason,omitempty"`
}

// HarnessMoveInspection is the read-only eligibility view for one-click move.
type HarnessMoveInspection struct {
	From   string             `json:"from"`
	Target string             `json:"target"`
	Groups []HarnessMoveGroup `json:"groups"`
}

// HarnessMoveOptions configures PrepareHarnessMove.
type HarnessMoveOptions struct {
	From                 string                       `json:"from"`
	Target               string                       `json:"target"`
	ProfileIDs           []string                     `json:"profile_ids,omitempty"` // empty = all ready groups
	RootsByInstance      map[string]map[string]string `json:"roots_by_instance,omitempty"`
	Source               string                       `json:"source,omitempty"`
	AllowDraft           bool                         `json:"allow_draft_profile,omitempty"`
	AllowSnapshotProfile bool                         `json:"allow_snapshot_profile,omitempty"`
	AllowVersionMismatch bool                         `json:"allow_version_mismatch,omitempty"`
	SkipService          bool                         `json:"skip_service,omitempty"`
}

// HarnessMoveGroupPlan holds the prepared plan (or preparation error) for one
// harness group.
type HarnessMoveGroupPlan struct {
	ProfileID     string             `json:"profile_id"`
	Group         HarnessMoveGroup   `json:"group"`
	BundlePlan    *BundleRestorePlan `json:"bundle_plan,omitempty"`
	SinglePlan    *RevivePlan        `json:"single_plan,omitempty"`
	PlanError     string             `json:"plan_error,omitempty"`
	PlanErrorCode string             `json:"plan_error_code,omitempty"`
}

// HarnessMovePlan is the ordered batch of harness groups to migrate.
type HarnessMovePlan struct {
	From   string                 `json:"from"`
	Target string                 `json:"target"`
	Groups []HarnessMoveGroupPlan `json:"groups"`
}

// Close releases all sub-plans.
func (p *HarnessMovePlan) Close() {
	if p == nil {
		return
	}
	for i := range p.Groups {
		if p.Groups[i].BundlePlan != nil {
			p.Groups[i].BundlePlan.Close()
		}
		if p.Groups[i].SinglePlan != nil {
			p.Groups[i].SinglePlan.Close()
		}
	}
}

// HarnessMoveGroupResult records the outcome of migrating one harness group.
type HarnessMoveGroupResult struct {
	ProfileID string                      `json:"profile_id"`
	Succeeded bool                        `json:"succeeded"`
	Partial   bool                        `json:"partial,omitempty"`
	Members   []BundleRestoreMemberResult `json:"members,omitempty"`
	Error     string                      `json:"error,omitempty"`
	ErrorCode string                      `json:"error_code,omitempty"`
}

// HarnessMoveResult summarizes the sequential harness-by-harness migration.
type HarnessMoveResult struct {
	From    string                   `json:"from"`
	Target  string                   `json:"target"`
	Groups  []HarnessMoveGroupResult `json:"groups"`
	Partial bool                     `json:"partial,omitempty"`
}

// buildHarnessMoveGroups groups active instances strictly by ProfileID (never
// by hardcoded harness names) and verifies that every member in a group has a
// full/raw backup (either a shared same-harness bundle or individual full/raw
// snapshots).
func buildHarnessMoveGroups(instances []enroll.InstanceSummary, snapshots []vault.Meta) []HarnessMoveGroup {
	byProfile := map[string][]enroll.InstanceSummary{}
	for _, inst := range instances {
		if inst.Superseded != nil || inst.ProfileID == "" {
			continue
		}
		byProfile[inst.ProfileID] = append(byProfile[inst.ProfileID], inst)
	}
	profileIDs := make([]string, 0, len(byProfile))
	for pid := range byProfile {
		profileIDs = append(profileIDs, pid)
	}
	sort.Strings(profileIDs)

	// Index full/raw snapshots by instance and by bundle id.
	fullByInst := map[string][]vault.Meta{}
	fullByBundle := map[string]map[string]vault.Meta{}
	for _, m := range snapshots {
		if !protection.FullTier(m.Tier) {
			continue
		}
		fullByInst[m.InstanceID] = append(fullByInst[m.InstanceID], m)
		if m.BundleID != "" {
			if fullByBundle[m.BundleID] == nil {
				fullByBundle[m.BundleID] = map[string]vault.Meta{}
			}
			fullByBundle[m.BundleID][m.InstanceID] = m
		}
	}
	for id := range fullByInst {
		sort.Slice(fullByInst[id], func(i, j int) bool {
			return fullByInst[id][i].CreatedAt.After(fullByInst[id][j].CreatedAt)
		})
	}

	groups := make([]HarnessMoveGroup, 0, len(profileIDs))
	for _, pid := range profileIDs {
		insts := byProfile[pid]
		// Order main agent (Name == "") first, then InstanceID.
		sort.SliceStable(insts, func(i, j int) bool {
			mi := insts[i].Name == ""
			mj := insts[j].Name == ""
			if mi != mj {
				return mi
			}
			return insts[i].InstanceID < insts[j].InstanceID
		})
		g := HarnessMoveGroup{ProfileID: pid, Ready: true}

		// Prefer a latest complete full/raw bundle covering every active member.
		bestBundleID := ""
		var bestBundleTime int64
		if len(insts) > 1 {
			for bid, bmap := range fullByBundle {
				ok := true
				var latest int64
				for _, inst := range insts {
					m, has := bmap[inst.InstanceID]
					if !has || m.ProfileID != pid {
						ok = false
						break
					}
					if ts := m.CreatedAt.UnixNano(); ts > latest {
						latest = ts
					}
				}
				if ok && latest > bestBundleTime {
					bestBundleTime = latest
					bestBundleID = bid
				}
			}
		}
		if bestBundleID != "" {
			g.BundleID = bestBundleID
			for _, inst := range insts {
				m := fullByBundle[bestBundleID][inst.InstanceID]
				mc := m
				g.Members = append(g.Members, HarnessMoveMember{
					InstanceID:  inst.InstanceID,
					Name:        inst.Name,
					DisplayName: inst.Label,
					Snapshot:    &mc,
					Ready:       true,
				})
			}
			groups = append(groups, g)
			continue
		}

		// Otherwise each member must have its own standalone full/raw snapshot.
		var missing []string
		for _, inst := range insts {
			mem := HarnessMoveMember{
				InstanceID:  inst.InstanceID,
				Name:        inst.Name,
				DisplayName: inst.Label,
			}
			for _, m := range fullByInst[inst.InstanceID] {
				if len(m.BundleMembers) <= 1 {
					mc := m
					mem.Snapshot = &mc
					mem.Ready = true
					break
				}
			}
			if !mem.Ready {
				g.Ready = false
				mem.Reason = "缺少完整的 full/raw 备份"
				missing = append(missing, inst.InstanceID)
			}
			g.Members = append(g.Members, mem)
		}
		if !g.Ready {
			g.Reason = fmt.Sprintf("成员 %s 缺少完整（full/raw）备份，无法一键搬家", strings.Join(missing, "、"))
		}
		groups = append(groups, g)
	}
	return groups
}

// InspectHarnessMove inspects active instances on from, groups them by
// ProfileID, and reports which harness groups are ready for one-click move.
func (s *Service) InspectHarnessMove(ctx context.Context, from, target string) (*HarnessMoveInspection, error) {
	if from == "" || target == "" {
		return nil, valise.New("request_invalid", "缺少源服务器或目标服务器", "选择源服务器和目标服务器")
	}
	if from == target {
		return nil, valise.New("mode_mismatch", "一键搬家需要选择另一台目标服务器", "选择不同的目标服务器")
	}
	listRes, err := s.ListAgents(ctx, AgentListRequest{ConnectionIDs: []string{from}})
	if err != nil {
		return nil, err
	}
	var insts []enroll.InstanceSummary
	for _, it := range listRes.Items {
		if it.ConnectionID == from {
			insts = append(insts, it.Instance)
		}
	}
	st, err := s.VaultStatus(ctx, from)
	if err != nil {
		return nil, err
	}
	return &HarnessMoveInspection{
		From:   from,
		Target: target,
		Groups: buildHarnessMoveGroups(insts, st.Snapshots),
	}, nil
}

// PrepareHarnessMove prepares migration plans for the selected (or all ready)
// harness groups. A planning failure in one group is recorded on that group
// and never prevents preparing the remaining groups.
func (s *Service) PrepareHarnessMove(ctx context.Context, o HarnessMoveOptions, passphrase string) (*HarnessMovePlan, error) {
	insp, err := s.InspectHarnessMove(ctx, o.From, o.Target)
	if err != nil {
		return nil, err
	}
	wanted := map[string]bool{}
	for _, pid := range o.ProfileIDs {
		wanted[pid] = true
	}
	plan := &HarnessMovePlan{
		From:   o.From,
		Target: o.Target,
	}
	for _, g := range insp.Groups {
		if len(wanted) > 0 && !wanted[g.ProfileID] {
			continue
		}
		gp := HarnessMoveGroupPlan{
			ProfileID: g.ProfileID,
			Group:     g,
		}
		if !g.Ready {
			gp.PlanErrorCode = "harness_not_ready"
			gp.PlanError = g.Reason
			plan.Groups = append(plan.Groups, gp)
			continue
		}
		if len(g.Members) > 1 || g.BundleID != "" {
			bMembers := make([]BundleRestoreMember, 0, len(g.Members))
			for _, m := range g.Members {
				bMembers = append(bMembers, BundleRestoreMember{
					InstanceID:  m.InstanceID,
					SnapshotID:  m.Snapshot.SnapshotID,
					Name:        m.Name,
					SkipService: m.Name != "",
				})
			}
			bp, perr := s.PrepareBundleRestore(ctx, BundleRestoreOptions{
				Target:               o.Target,
				From:                 o.From,
				Mode:                 revive.ModeMigrate,
				BundleID:             g.BundleID,
				Members:              bMembers,
				RootsByInstance:      o.RootsByInstance,
				Source:               o.Source,
				AllowDraft:           o.AllowDraft,
				AllowSnapshotProfile: o.AllowSnapshotProfile,
				AllowVersionMismatch: o.AllowVersionMismatch,
				SkipService:          o.SkipService,
			}, passphrase)
			if perr != nil {
				gp.PlanError = perr.Error()
				if ue, ok := valise.As(perr); ok {
					gp.PlanErrorCode = ue.Code
					gp.PlanError = ue.Reason
				}
			} else {
				gp.BundlePlan = bp
			}
		} else if len(g.Members) == 1 {
			m := g.Members[0]
			rp, perr := s.PlanRevive(ctx, ReviveOptions{
				Target:               o.Target,
				From:                 o.From,
				SnapshotID:           m.Snapshot.SnapshotID,
				Mode:                 revive.ModeMigrate,
				Roots:                o.RootsByInstance[m.InstanceID],
				Source:               o.Source,
				AllowDraft:           o.AllowDraft,
				AllowSnapshotProfile: o.AllowSnapshotProfile,
				AllowVersionMismatch: o.AllowVersionMismatch,
				SkipService:          o.SkipService,
			}, passphrase)
			if perr != nil {
				gp.PlanError = perr.Error()
				if ue, ok := valise.As(perr); ok {
					gp.PlanErrorCode = ue.Code
					gp.PlanError = ue.Reason
				}
			} else {
				gp.SinglePlan = rp
			}
		}
		plan.Groups = append(plan.Groups, gp)
	}
	return plan, nil
}

// RunHarnessMove executes prepared harness groups sequentially, one harness at
// a time. A failure in one harness is recorded and execution continues with
// the remaining harnesses.
func (s *Service) RunHarnessMove(ctx context.Context, plan *HarnessMovePlan, confirm ReviveConfirm) (*HarnessMoveResult, error) {
	return s.runHarnessMoveWith(ctx, plan, confirm, nil)
}

type harnessGroupRunner func(ctx context.Context, gp HarnessMoveGroupPlan, confirm ReviveConfirm) HarnessMoveGroupResult

func (s *Service) runHarnessMoveWith(ctx context.Context, plan *HarnessMovePlan, confirm ReviveConfirm, runFn harnessGroupRunner) (*HarnessMoveResult, error) {
	if plan == nil || len(plan.Groups) == 0 {
		return nil, valise.New("move_plan_empty", "没有可搬家的 Harness 分组", "先检查各 Harness 的完整备份状态")
	}
	res := &HarnessMoveResult{
		From:   plan.From,
		Target: plan.Target,
		Groups: make([]HarnessMoveGroupResult, 0, len(plan.Groups)),
	}
	for _, gp := range plan.Groups {
		var gr HarnessMoveGroupResult
		if gp.PlanError != "" {
			gr = HarnessMoveGroupResult{
				ProfileID: gp.ProfileID,
				Succeeded: false,
				Error:     gp.PlanError,
				ErrorCode: gp.PlanErrorCode,
			}
		} else if runFn != nil {
			gr = runFn(ctx, gp, confirm)
		} else {
			gr = s.runOneHarnessGroup(ctx, gp, confirm)
		}
		if !gr.Succeeded || gr.Partial {
			res.Partial = true
		}
		res.Groups = append(res.Groups, gr)
	}
	return res, nil
}

func (s *Service) runOneHarnessGroup(ctx context.Context, gp HarnessMoveGroupPlan, confirm ReviveConfirm) HarnessMoveGroupResult {
	gr := HarnessMoveGroupResult{ProfileID: gp.ProfileID}
	switch {
	case gp.BundlePlan != nil:
		bres, err := s.RunBundleRestore(ctx, gp.BundlePlan, confirm)
		if err != nil {
			gr.Error = err.Error()
			if ue, ok := valise.As(err); ok {
				gr.ErrorCode = ue.Code
				gr.Error = ue.Reason
			}
			return gr
		}
		gr.Members = bres.Members
		gr.Partial = bres.Partial
		gr.Succeeded = !bres.Partial
		if bres.Partial {
			for _, m := range bres.Members {
				if m.Error != "" {
					gr.Error = m.Error
					gr.ErrorCode = m.ErrorCode
					break
				}
			}
		}
		return gr
	case gp.SinglePlan != nil:
		mr := BundleRestoreMemberResult{
			InstanceID: gp.SinglePlan.Plan.InstanceID,
			SnapshotID: gp.SinglePlan.Snapshot.SnapshotID,
		}
		swapRes, err := s.ExecuteReviveConfirmed(ctx, gp.SinglePlan, "", confirm)
		mr.Result = swapRes
		if err != nil {
			mr.Error = err.Error()
			if ue, ok := valise.As(err); ok {
				mr.ErrorCode = ue.Code
				mr.Error = ue.Reason
			}
			gr.Error = mr.Error
			gr.ErrorCode = mr.ErrorCode
		} else {
			gr.Succeeded = true
		}
		gr.Members = []BundleRestoreMemberResult{mr}
		return gr
	default:
		gr.ErrorCode = "move_plan_empty"
		gr.Error = "该 Harness 分组没有可执行的恢复计划"
		return gr
	}
}
