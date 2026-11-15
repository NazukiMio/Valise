package service

import (
	"valise/core/enroll"
	"valise/core/profile"
)

// serviceRepair accepts only the bounded, explicitly associated B02 action.
func serviceRepair(p profile.Profile) *profile.Action {
	if p.Service == nil || len(p.Service.Candidates) == 0 {
		return nil
	}
	a := p.FindAction(p.Service.RepairAction)
	if a == nil || a.Risk != profile.RiskManage || a.Terminal || a.Output == profile.OutputSecret {
		return nil
	}
	return a
}

func (s *Service) bindServiceRepair(it *HealthItem, in enroll.InstanceSummary) {
	if in.Runtime != nil {
		return // The old native service action cannot control a managed entry.
	}
	if it.Level != HealthBad && it.Level != HealthWarn {
		return
	}
	set, err := s.Profiles()
	if err != nil || set.ByID[in.ProfileID] == nil {
		return
	}
	p := set.ByID[in.ProfileID].Effective.ForAgent(in.Name)
	if a := serviceRepair(p); a != nil {
		it.ActionID, it.ActionName, it.Key = a.ID, a.Name, "f"
	}
}

// HealthVerification is independent of the action's command exit code and
// dismissals. Unknown never means the problem was resolved.
type HealthVerification struct {
	ItemID string `json:"item_id"`
	State  string `json:"state"` // resolved, remaining, unknown
	Text   string `json:"text"`
}

func verifyService(r *HealthReport, err error) *HealthVerification {
	v := &HealthVerification{ItemID: "service", State: "unknown", Text: "无法复核，请检查服务器连接后刷新健康页"}
	if err != nil || r == nil {
		return v
	}
	for _, c := range r.Checks {
		if c.ID == "service" && c.State != "ok" {
			return v
		}
	}
	for _, it := range r.Items {
		if it.ID != "service" {
			continue
		}
		switch it.Level {
		case HealthBad, HealthWarn:
			v.State, v.Text = "remaining", "问题仍在："+it.Text
		case HealthOK:
			v.State, v.Text = "resolved", "本次检查通过："+it.Text
		default:
			v.Text = "没有可复核的服务或管理页，请检查 harness 资料后重新接入"
		}
	}
	return v
}
