package service

import (
	"context"
	"slices"

	"valise/core/identity"
	"valise/core/protection"
	"valise/core/valise"
)

// This client's binding for one instance: whether each local repository is
// filled after a backup made here and when Valise opens
// (docs/ARCHITECTURE.md 7.4, contracts 4.1).

// LocalCopiesView is the binding as a front end shows it.
type LocalCopiesView struct {
	Agent identity.AgentRef `json:"agent_ref"`
	// Planned is false while the instance has not adopted a protection
	// plan: no binding can be saved and the old rule applies.
	Planned bool `json:"planned"`
	// Binding is nil when none is saved: every repository both times.
	Binding *protection.ClientBinding `json:"binding,omitempty"`
}

// LocalCopyChange sets one repository; a nil field keeps its value.
type LocalCopyChange struct {
	Repo         string
	AfterBackup  *bool
	OnClientOpen *bool
}

// LocalCopies reads the instance's binding.
func (s *Service) LocalCopies(ctx context.Context, host, instance string) (*LocalCopiesView, error) {
	v, _, err := s.localCopiesView(ctx, host, instance)
	return v, err
}

func (s *Service) localCopiesView(ctx context.Context, host, instance string) (*LocalCopiesView, protection.Revision, error) {
	refs, err := s.agentRefs(ctx, host, []string{instance})
	if err != nil {
		return nil, 0, err
	}
	ref, ok := refs[instance]
	if !ok {
		return nil, 0, valise.New("identity_unbound", "实例 "+instance+" 还没有身份，不能设置本客户端的复制", "先接入实例")
	}
	pv, err := s.GetProtection(ctx, host, protection.GetParams{AgentRef: ref})
	if err != nil {
		return nil, 0, err
	}
	v := &LocalCopiesView{Agent: ref, Planned: pv.ServerPlan != nil}
	var serverRev protection.Revision
	if pv.ServerPlan != nil {
		serverRev = pv.ServerPlan.Revision
	}
	v.Binding, err = NewProtectionBindingStore(s.Dirs.Data, s.Config.Repos).Read(ctx, ref)
	return v, serverRev, err
}

// SetLocalCopies applies changes to the instance's binding, compared by
// version. The first save starts from every registered repository both
// times (the old rule), so nothing is switched off unasked.
func (s *Service) SetLocalCopies(ctx context.Context, host, instance string, changes []LocalCopyChange) (*protection.ClientBinding, error) {
	if len(changes) == 0 {
		return nil, valise.New("request_invalid", "没有要修改的仓库", "用 --repo 指定仓库")
	}
	v, serverRev, err := s.localCopiesView(ctx, host, instance)
	if err != nil {
		return nil, err
	}
	if !v.Planned {
		return nil, valise.New("protection_not_adopted", "实例 "+instance+" 还没有采用保护计划，按原规则补到所有仓库", "先采用保护计划，再设置本客户端的复制")
	}
	var copies []protection.LocalCopy
	var expected protection.Revision
	if v.Binding != nil {
		copies, expected = slices.Clone(v.Binding.LocalCopies), v.Binding.Revision
	} else {
		for _, r := range s.Config.Repos {
			on, on2 := true, true
			copies = append(copies, protection.LocalCopy{RepoID: r.Name, AfterBackup: &on, OnClientOpen: &on2})
		}
	}
	for _, ch := range changes {
		if _, err := s.repo(ch.Repo); err != nil {
			return nil, err
		}
		i := slices.IndexFunc(copies, func(c protection.LocalCopy) bool { return c.RepoID == ch.Repo })
		if i < 0 {
			off, off2 := false, false
			copies = append(copies, protection.LocalCopy{RepoID: ch.Repo, AfterBackup: &off, OnClientOpen: &off2})
			i = len(copies) - 1
		}
		if ch.AfterBackup != nil {
			b := *ch.AfterBackup
			copies[i].AfterBackup = &b
		}
		if ch.OnClientOpen != nil {
			b := *ch.OnClientOpen
			copies[i].OnClientOpen = &b
		}
	}
	return NewProtectionBindingStore(s.Dirs.Data, s.Config.Repos).CompareAndSwap(ctx, v.Agent, expected, serverRev, copies)
}
