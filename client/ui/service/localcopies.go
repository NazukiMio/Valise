package service

import (
	"context"
	"slices"

	"valise/client/config"
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
	// Repos and Clouds are the effective per-location choices: the saved
	// ones, with every registered location defaulting to both times on (a
	// repo missing from a saved binding is off; a cloud missing is on).
	Repos  []protection.LocalCopy `json:"repos"`
	Clouds []protection.LocalCopy `json:"clouds"`
}

// LocalCopyChange sets one repository; a nil field keeps its value.
type LocalCopyChange struct {
	// Kind is "" or "repo" (Repo names a repository) or "cloud" (Repo names
	// a registered cloud).
	Kind         string
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
	v.Binding, err = s.bindingStore().Read(ctx, ref)
	if err != nil {
		return v, serverRev, err
	}
	v.Repos, v.Clouds = effectiveCopies(s.Config, v.Binding)
	return v, serverRev, nil
}

func (s *Service) bindingStore() *ProtectionBindingStore {
	return NewProtectionBindingStore(s.Dirs.Data, s.Config.Repos).WithClouds(s.Config.Clouds)
}

func boolPtr(b bool) *bool { return &b }

// effectiveCopies resolves the binding over the registered locations.
func effectiveCopies(cfg config.Config, b *protection.ClientBinding) (repos, clouds []protection.LocalCopy) {
	repos, clouds = []protection.LocalCopy{}, []protection.LocalCopy{}
	find := func(list []protection.LocalCopy, name string) *protection.LocalCopy {
		i := slices.IndexFunc(list, func(c protection.LocalCopy) bool { return c.RepoID == name })
		if i < 0 {
			return nil
		}
		return &list[i]
	}
	for _, r := range cfg.Repos {
		c := protection.LocalCopy{RepoID: r.Name, AfterBackup: boolPtr(b == nil), OnClientOpen: boolPtr(b == nil)}
		if b != nil {
			if e := find(b.LocalCopies, r.Name); e != nil {
				c = *e
			}
		}
		repos = append(repos, c)
	}
	for _, cl := range cfg.Clouds {
		c := protection.LocalCopy{RepoID: cl.Name, AfterBackup: boolPtr(true), OnClientOpen: boolPtr(true)}
		if b != nil {
			if e := find(b.CloudCopies, cl.Name); e != nil {
				c = *e
			}
		}
		clouds = append(clouds, c)
	}
	return repos, clouds
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
	var expected protection.Revision
	if v.Binding != nil {
		expected = v.Binding.Revision
	}
	copies, clouds := slices.Clone(v.Repos), slices.Clone(v.Clouds)
	for i := range copies {
		copies[i] = cloneProtection(copies[i])
	}
	for i := range clouds {
		clouds[i] = cloneProtection(clouds[i])
	}
	cloudTouched := false
	for _, ch := range changes {
		list := &copies
		switch ch.Kind {
		case "", "repo":
			if _, err := s.repo(ch.Repo); err != nil {
				return nil, err
			}
		case "cloud":
			if s.cloudHere(ch.Repo) == nil {
				return nil, valise.New("cloud_unknown", "没有叫 "+ch.Repo+" 的云盘", "用 valise cloud list 查看")
			}
			list, cloudTouched = &clouds, true
		default:
			return nil, valise.New("request_invalid", "未知的复制位置类型："+ch.Kind, "用 repo 或 cloud")
		}
		i := slices.IndexFunc(*list, func(c protection.LocalCopy) bool { return c.RepoID == ch.Repo })
		if i < 0 {
			*list = append(*list, protection.LocalCopy{RepoID: ch.Repo, AfterBackup: boolPtr(false), OnClientOpen: boolPtr(false)})
			i = len(*list) - 1
		}
		if ch.AfterBackup != nil {
			(*list)[i].AfterBackup = boolPtr(*ch.AfterBackup)
		}
		if ch.OnClientOpen != nil {
			(*list)[i].OnClientOpen = boolPtr(*ch.OnClientOpen)
		}
	}
	// Cloud choices are only written once one is touched or already saved, so
	// bindings of older clients stay byte-compatible.
	if !cloudTouched && (v.Binding == nil || v.Binding.CloudCopies == nil) {
		clouds = nil
	}
	if copies == nil {
		copies = []protection.LocalCopy{}
	}
	return s.bindingStore().CompareAndSwapCopies(ctx, v.Agent, expected, serverRev, copies, clouds)
}
