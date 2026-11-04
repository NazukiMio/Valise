package service

import (
	"context"
	"strings"
	"time"

	"valise/client/porter"
	"valise/client/ssh"
	"valise/core/action"
	"valise/core/enroll"
	"valise/core/profile"
	"valise/core/valise"
	"valise/core/vault"
	"valise/core/vaultrpc"
)

// Managed actions (docs/INSTALL.md section 5). The client names an action;
// the porter looks its command up in the profile stored on the server.

// RiskLabels are the plain names of the risk levels.
var RiskLabels = map[string]string{
	profile.RiskRead:   "查看",
	profile.RiskManage: "常规管理",
	profile.RiskConfig: "修改配置",
	profile.RiskDanger: "高风险",
}

// ActionList is an instance's actions as the local profile declares them.
type ActionList struct {
	Instance  string           `json:"instance"`
	ProfileID string           `json:"profile_id"`
	Actions   []profile.Action `json:"actions"`
	// Stale is set when the server's stored profile is another version:
	// actions added since then need a re-enrollment.
	Stale        bool   `json:"stale"`
	RepairAction string `json:"repair_action,omitempty"`
}

// Actions lists the instance's managed actions.
func (s *Service) Actions(ctx context.Context, host, instance string) (*ActionList, error) {
	target, err := s.LoginMethods(ctx, host, instance)
	if err != nil {
		return nil, err
	}
	p := target.Profile.ForAgent(target.Record.Name)
	list := &ActionList{Instance: instance, ProfileID: p.ID, Actions: p.Actions,
		Stale: target.Record.ProfileVersion != p.ProfileVersion}
	if a := serviceRepair(p); a != nil {
		list.RepairAction = a.ID
	}
	return list, nil
}

// ActionOptions control RunAction.
type ActionOptions struct {
	// Seal takes an escape pod first (config and danger actions).
	Seal bool
	// IO is the terminal of a terminal action.
	IO       InstallIO
	Progress func(string)
}

// ActionResult is the outcome: the command's exit code and, separately,
// the instance's state afterwards.
type ActionResult struct {
	action.RunResult
	Snapshot     string                `json:"snapshot,omitempty"`
	Checked      bool                  `json:"checked"`
	Service      []enroll.ServiceState `json:"service,omitempty"`
	Entries      []enroll.EntryState   `json:"entries,omitempty"`
	Verification *HealthVerification   `json:"verification,omitempty"`
}

// actionSettle is how long the instance may take to come back after a
// manage or stronger action.
var actionSettle = 20 * time.Second

// RunAction runs one action of the instance.
func (s *Service) RunAction(ctx context.Context, host, instance, id string, o ActionOptions) (*ActionResult, error) {
	list, err := s.Actions(ctx, host, instance)
	if err != nil {
		return nil, err
	}
	var a *profile.Action
	for i := range list.Actions {
		if list.Actions[i].ID == id {
			a = &list.Actions[i]
		}
	}
	if a == nil {
		return nil, valise.New("action_unknown", list.ProfileID+" 的 harness 资料里没有动作 "+id, "不带动作名运行 valise action <服务器> <实例> 查看可用的动作")
	}
	if list.Stale {
		return nil, valise.New("action_profile_stale", "本机与服务器的 harness 资料版本不同，动作没有执行", "先在设置中重新接入，再查看并确认动作")
	}
	if a.Risk == profile.RiskConfig || a.Risk == profile.RiskDanger {
		if err := s.RequireGate(ctx, host, instance); err != nil {
			return nil, err
		}
	}
	progress := func(m string) {
		if o.Progress != nil {
			o.Progress(m)
		}
	}
	res := &ActionResult{}
	if o.Seal {
		progress("建立逃生舱（改动之前）")
		rep, err := s.Seal(ctx, host, vaultrpc.SealParams{InstanceID: instance, Tier: vault.TierSoulSecrets, Reason: "管理动作之前：" + a.Name})
		if err != nil {
			return nil, err
		}
		res.Snapshot = rep.SnapshotID
	}
	if a.Terminal {
		t, done, err := s.adminTransport(ctx, host)
		if err != nil {
			return nil, err
		}
		defer done()
		found, err := porter.Probe(ctx, t)
		if err != nil {
			return nil, err
		}
		line := strings.Join([]string{ssh.Quote(found.Path), "action-exec", "--instance", ssh.Quote(instance), "--id", ssh.Quote(id)}, " ")
		if res.Exit, err = runLineTTY(ctx, t, line, o.IO); err != nil {
			return nil, err
		}
	} else {
		var rr action.RunResult
		if err := s.withSession(ctx, host, func(c caller) error {
			return c.Call(ctx, action.MethodRun, action.RunParams{InstanceID: instance, ActionID: id, Sealed: o.Seal}, &rr)
		}); err != nil {
			return nil, err
		}
		res.RunResult = rr
	}
	if a.Risk != profile.RiskRead {
		progress("检查服务与管理页")
		s.settle(ctx, host, instance, res)
	}
	if list.RepairAction == id {
		r, err := s.Health(ctx, host, instance)
		res.Verification = verifyService(r, err)
	}
	return res, nil
}

// settle reads the instance's live state until every service is active and
// every entry listens, or actionSettle passes.
func (s *Service) settle(ctx context.Context, host, instance string, res *ActionResult) {
	deadline := time.Now().Add(actionSettle)
	for {
		list, err := s.Instances(ctx, host)
		if err != nil {
			return
		}
		for _, in := range list {
			if in.InstanceID == instance {
				res.Checked, res.Service, res.Entries = true, in.Service, in.Entries
			}
		}
		healthy := res.Checked
		for _, sv := range res.Service {
			healthy = healthy && (sv.LoadState != "loaded" || sv.Active == "active")
		}
		for _, e := range res.Entries {
			healthy = healthy && e.Listening
		}
		if healthy || time.Now().After(deadline) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

// ActionAccess says which login an action needs on host, in the words of
// that server's setup (docs/ARCHITECTURE.md 2.1), for the confirmation.
func (s *Service) ActionAccess(host string, a profile.Action) string {
	if a.AccessTier() == "owner" {
		if !s.Tiered(host) {
			return "用你自己的 SSH 登录"
		}
		return "要用 Valise 口令确认是你本人"
	}
	if !a.Terminal && a.Output != profile.OutputSecret && !a.NeedsAdmin() {
		if !s.Tiered(host) {
			return "用你自己的 SSH 登录"
		}
		return "日常钥匙即可"
	}
	login := "要输入服务器密码（敏感操作）"
	switch {
	case !s.Tiered(host):
		login = "用你自己的 SSH 登录"
	case s.HasAdminKey(host):
		login = "要解锁管理钥匙（管理口令）"
	}
	switch {
	case a.Terminal:
		return login + " · 在独立终端里运行"
	case a.Output == profile.OutputSecret:
		return login + " · 内容不显示"
	}
	return login
}

// SealsByDefault reports whether the action takes an escape pod first
// unless the user unticks it.
func SealsByDefault(a profile.Action) bool {
	return a.Risk == profile.RiskConfig || a.Risk == profile.RiskDanger
}
