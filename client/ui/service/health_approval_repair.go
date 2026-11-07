package service

import "context"

func (s *Service) bindApprovalRepair(ctx context.Context, host, instance string, r *HealthReport) {
	set, err := s.Profiles()
	if err != nil {
		return
	}
	declared := false
	for _, p := range set.ByID {
		if p.Effective.ApprovalCheck != nil && p.Effective.ApprovalCheck.RepairAction != "" {
			declared = true
			break
		}
	}
	if !declared {
		return
	}
	list, err := s.Actions(ctx, host, instance)
	if err != nil || list.Stale || list.ApprovalRepairAction == "" {
		return
	}
	for i := range r.Items {
		it := &r.Items[i]
		if it.ID != "approval" || it.Level != HealthBad {
			continue
		}
		for _, a := range list.Actions {
			if a.ID == list.ApprovalRepairAction {
				it.ActionID, it.ActionName, it.Key = a.ID, a.Name, "f"
			}
		}
	}
}
