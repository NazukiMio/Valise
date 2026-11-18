package service

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"valise/core/identity"
	"valise/core/protection"
	"valise/core/valise"
)

// TargetBlocker is an accepted protection plan on a server that still copies
// to a backup location, preventing that location from being removed.
type TargetBlocker struct {
	Host       string              `json:"host"`
	InstanceID string              `json:"instance_id"`
	Label      string              `json:"label,omitempty"`
	AgentRef   identity.AgentRef   `json:"agent_ref"`
	Revision   protection.Revision `json:"revision"`
	Desired    protection.Desired  `json:"desired"`
}

// TargetScanUnread records a server or instance whose protection plans could
// not be read while scanning for references to a backup location.
type TargetScanUnread struct {
	Host       string `json:"host"`
	InstanceID string `json:"instance_id,omitempty"`
	Err        error  `json:"-"`
}

// TargetRemovalScan summarizes all active protection plans across readable
// servers that block removing a backup location.
type TargetRemovalScan struct {
	Target    string             `json:"target"`
	OnServers []string           `json:"on_servers,omitempty"`
	Blockers  []TargetBlocker    `json:"blockers,omitempty"`
	Unread    []TargetScanUnread `json:"unread,omitempty"`
}

// UnblockOptions configures sequential protection-plan updates when clearing
// references to a backup location before removal.
type UnblockOptions struct {
	// ConfirmPlan is invoked once a change is prepared and before
	// StartProtectionChange runs, allowing an interactive window to record
	// the user's explicit confirmation for the prepared PlanID.
	ConfirmPlan func(host string, fc protection.FrozenChange)
	// Step is invoked before updating each blocker.
	Step func(b TargetBlocker, index, total int)
}

// DesiredWithoutTarget returns a copy of d with every ServerCopies reference
// to targetName removed, keeping an explicit non-nil slice.
func DesiredWithoutTarget(d protection.Desired, targetName string) protection.Desired {
	keep := make([]protection.ServerCopy, 0, len(d.ServerCopies))
	for _, cp := range d.ServerCopies {
		if cp.TargetRef.ID != targetName {
			keep = append(keep, cp)
		}
	}
	d.ServerCopies = keep
	return d
}

func planUsesTarget(d protection.Desired, targetName string) bool {
	for _, cp := range d.ServerCopies {
		if cp.TargetRef.ID == targetName {
			return true
		}
	}
	return false
}

// ScanTargetRemoval scans all currently readable servers and collects every
// active protection plan whose server_copies still reference name. Unreachable
// servers are reported in Unread without hiding blockers found elsewhere.
func (s *Service) ScanTargetRemoval(ctx context.Context, name string) (*TargetRemovalScan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	servers, err := s.ServerList()
	if err != nil {
		return nil, err
	}
	scan := &TargetRemovalScan{Target: name}
	if len(servers) == 0 {
		return scan, nil
	}
	agents, err := s.ListAgents(ctx, AgentListRequest{})
	if err != nil {
		return nil, err
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	slots := make(chan struct{}, 4)
	for _, obs := range agents.Observations {
		if obs.Status != "fresh" || obs.Value == nil {
			var uerr error
			if obs.Error != nil {
				uerr = obs.Error
			}
			mu.Lock()
			scan.Unread = append(scan.Unread, TargetScanUnread{Host: obs.ConnectionID, Err: uerr})
			mu.Unlock()
			continue
		}
		wg.Add(1)
		go func(obs AgentServerObservation) {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				mu.Lock()
				scan.Unread = append(scan.Unread, TargetScanUnread{Host: obs.ConnectionID, Err: ctx.Err()})
				mu.Unlock()
				return
			}
			defer func() { <-slots }()
			onServer, blockers, unread := s.scanServerTargetRemoval(ctx, name, obs)
			mu.Lock()
			if onServer {
				scan.OnServers = append(scan.OnServers, obs.ConnectionID)
			}
			scan.Blockers = append(scan.Blockers, blockers...)
			scan.Unread = append(scan.Unread, unread...)
			mu.Unlock()
		}(obs)
	}
	wg.Wait()
	sort.Strings(scan.OnServers)
	sort.Slice(scan.Blockers, func(i, j int) bool {
		if scan.Blockers[i].Host != scan.Blockers[j].Host {
			return scan.Blockers[i].Host < scan.Blockers[j].Host
		}
		return scan.Blockers[i].InstanceID < scan.Blockers[j].InstanceID
	})
	sort.Slice(scan.Unread, func(i, j int) bool {
		if scan.Unread[i].Host != scan.Unread[j].Host {
			return scan.Unread[i].Host < scan.Unread[j].Host
		}
		return scan.Unread[i].InstanceID < scan.Unread[j].InstanceID
	})
	return scan, nil
}

func (s *Service) scanServerTargetRemoval(ctx context.Context, name string, obs AgentServerObservation) (onServer bool, blockers []TargetBlocker, unread []TargetScanUnread) {
	st, err := s.VaultStatus(ctx, obs.ConnectionID)
	if err != nil {
		unread = append(unread, TargetScanUnread{Host: obs.ConnectionID, Err: err})
	} else {
		for _, tg := range st.Targets {
			if tg.Name == name {
				onServer = true
				break
			}
		}
	}
	for _, item := range *obs.Value {
		if item.IdentityState == "identity_conflict" {
			unread = append(unread, TargetScanUnread{Host: obs.ConnectionID, InstanceID: item.Instance.InstanceID, Err: agentReadError("identity_conflict")})
			continue
		}
		if item.AgentRef == nil {
			continue
		}
		view, err := s.GetProtection(ctx, obs.ConnectionID, protection.GetParams{AgentRef: *item.AgentRef})
		if err != nil {
			if ue, ok := valise.As(err); ok && ue.Code == "unsupported_capability" && strings.Contains(ue.Reason, protection.Capability) {
				continue
			}
			unread = append(unread, TargetScanUnread{Host: obs.ConnectionID, InstanceID: item.Instance.InstanceID, Err: err})
			continue
		}
		if view.Mode == "accepted" && view.ServerPlan != nil && planUsesTarget(view.ServerPlan.Desired, name) {
			blockers = append(blockers, TargetBlocker{
				Host:       obs.ConnectionID,
				InstanceID: item.Instance.InstanceID,
				Label:      item.Instance.Label,
				AgentRef:   *item.AgentRef,
				Revision:   view.ServerPlan.Revision,
				Desired:    view.ServerPlan.Desired,
			})
		}
	}
	return onServer, blockers, unread
}

// UnblockTarget updates each blocker's protection plan in turn to remove
// targetName from server_copies. It stops on the first failure and returns the
// remaining un-updated blockers along with a descriptive error.
func (s *Service) UnblockTarget(ctx context.Context, targetName string, blockers []TargetBlocker, opts UnblockOptions) ([]TargetBlocker, error) {
	stopOp := s.BeginOperation()
	defer stopOp()
	for i, b := range blockers {
		if err := ctx.Err(); err != nil {
			return blockers[i:], err
		}
		if opts.Step != nil {
			opts.Step(b, i, len(blockers))
		}
		desired := DesiredWithoutTarget(b.Desired, targetName)
		fc, err := s.PrepareProtectionChange(ctx, b.Host, protection.PrepareParams{
			AgentRef:         b.AgentRef,
			ExpectedRevision: b.Revision,
			Desired:          desired,
		})
		if err != nil {
			return blockers[i:], wrapTargetUnblockError(b, err)
		}
		if opts.ConfirmPlan != nil {
			opts.ConfirmPlan(b.Host, fc)
		}
		task, err := s.StartProtectionChange(ctx, fc.PlanID, "target-unblock-"+fc.PlanID)
		for err == nil && task.State == "running" {
			select {
			case <-ctx.Done():
				err = ctx.Err()
			case <-time.After(250 * time.Millisecond):
				task, err = s.GetProtectionTask(ctx, task.TaskRef)
			}
		}
		if err != nil {
			return blockers[i:], wrapTargetUnblockError(b, err)
		}
		if task.State == "cancelled" {
			return blockers[i:], wrapTargetUnblockError(b, valise.New("cancelled", "已取消修改保护计划", "如需移除该位置，请重新确认更新上述保护计划"))
		}
		if task.Error != nil {
			return blockers[i:], wrapTargetUnblockError(b, valise.New(task.Error.Code, task.Error.Reason, task.Error.Suggestion))
		}
		if task.State != "succeeded" {
			return blockers[i:], wrapTargetUnblockError(b, valise.New("protection_change_failed", "保护计划未完成更新（"+task.State+"）", "重新读取保护设置后重试"))
		}
	}
	return nil, nil
}

func wrapTargetUnblockError(b TargetBlocker, err error) error {
	who := b.Host + " / " + b.InstanceID
	if b.Label != "" && b.Label != b.InstanceID {
		who = b.Host + " / " + b.Label + "（" + b.InstanceID + "）"
	}
	if ue, ok := valise.As(err); ok {
		return valise.New(ue.Code, who+"："+ue.Reason, ue.Suggestion).WithDetail(ue.Detail)
	}
	return valise.New("protection_change_failed", "未能更新 "+who+" 的保护计划", "检查服务器连接和保护设置后重试").WithDetail(err.Error())
}
