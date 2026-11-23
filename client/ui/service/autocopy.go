package service

import (
	"context"
	"errors"
	"slices"
	"strings"

	"valise/core/identity"
	"valise/core/protection"
	"valise/core/valise"
	"valise/core/vault"
	"valise/core/vaultrpc"
)

// Copies only this computer can make (docs/ARCHITECTURE.md 7.4): local
// repositories and clouds logged in here, filled when Valise opens and
// after a backup made here. Every front end calls AutoCopy; none keeps its
// own rules.

// CopyTrigger is why an automatic copy runs.
type CopyTrigger string

const (
	TriggerOpen   CopyTrigger = "open"
	TriggerBackup CopyTrigger = "backup"
)

// Outcome states of one location.
const (
	CopyCopied    = "copied"    // new backups copied
	CopyCurrent   = "current"   // nothing new
	CopyOff       = "off"       // this client's binding says not now
	CopyUnmounted = "unmounted" // the repository folder is not there
	CopyBusy      = "busy"      // another Valise is copying to it
	CopyFailed    = "failed"
)

// AutoCopyOutcome is one location's result.
type AutoCopyOutcome struct {
	Location string `json:"location"`
	Kind     string `json:"kind"` // "repo" or "cloud"
	State    string `json:"state"`
	// Copied counts backups newly in the repository, or periods copied to
	// a cloud (where the copy tool does not count backups).
	Copied     int    `json:"copied,omitempty"`
	Path       string `json:"path,omitempty"` // cloud: direct or a relay
	Reason     string `json:"reason,omitempty"`
	Suggestion string `json:"suggestion,omitempty"`
}

// AutoCopyResult is one trigger's run for a server.
type AutoCopyResult struct {
	Host      string            `json:"host"`
	Trigger   CopyTrigger       `json:"trigger"`
	Locations []AutoCopyOutcome `json:"locations"`
	// Certificates of remote locations (open only).
	CertsRenewed  []string `json:"certs_renewed,omitempty"`
	CertsExpiring []string `json:"certs_expiring,omitempty"`
	CertError     string   `json:"cert_error,omitempty"`
	// LostSource reminds of migrations made while this server was lost
	// (open only, docs/ARCHITECTURE.md 8.2.3).
	LostSource []string `json:"lost_source,omitempty"`
}

// Changed reports whether anything was copied or failed: the only results
// a front end interrupts the person for.
func (r *AutoCopyResult) Changed() bool {
	for _, o := range r.Locations {
		if o.State == CopyCopied || o.State == CopyFailed {
			return true
		}
	}
	return len(r.CertsRenewed) > 0 || len(r.CertsExpiring) > 0 || r.CertError != "" || len(r.LostSource) > 0
}

// AutoCopyOptions narrow a run. Progress, when set, sees each location
// as it starts (CLI in the foreground).
type AutoCopyOptions struct {
	// Instance is the one whose backup just succeeded (TriggerBackup).
	Instance string
	Progress func(location string)
}

// AutoCopy runs one trigger for host. It never retries within the run: a
// failure is recorded and the next trigger tries again; copies only append,
// so an interrupted one continues from what is there.
func (s *Service) AutoCopy(ctx context.Context, host string, trigger CopyTrigger, o AutoCopyOptions) (*AutoCopyResult, error) {
	if trigger != TriggerOpen && trigger != TriggerBackup {
		return nil, valise.New("request_invalid", "未知的自动复制时机", "")
	}
	if s.RecoveryMode() {
		return nil, valise.New("recovery_mode", "正在找回 Valise 口令，自动复制暂停", "找回完成或取消后再复制")
	}
	res := &AutoCopyResult{Host: host, Trigger: trigger, Locations: []AutoCopyOutcome{}}
	if trigger == TriggerOpen {
		s.renewForOpen(ctx, host, res)
		for _, w := range s.LostSourceWarnings(ctx, host) {
			res.LostSource = append(res.LostSource, w.Text)
		}
	}
	if len(s.Config.Repos) == 0 && len(s.Config.Clouds) == 0 {
		return res, nil
	}
	st, err := s.VaultStatus(ctx, host)
	if err != nil {
		return res, err
	}
	if !st.Exists || st.ServerName == "" {
		return res, nil
	}
	insts := instancesOf(st.Leaves)
	wanted := s.wantedRepos(ctx, host, trigger, o.Instance, insts)
	wantedCloud := s.wantedClouds(ctx, host, trigger, o.Instance, insts)
	for _, r := range s.Config.Repos {
		out := AutoCopyOutcome{Location: r.Name, Kind: "repo"}
		switch {
		case !wanted(r.Name):
			out.State = CopyOff
		case !s.repoAvailable(r.Path):
			out.State, out.Reason = CopyUnmounted, "文件夹现在不在："+r.Path
			out.Suggestion = "接上外接硬盘后，下次打开 Valise 时会补齐"
		default:
			if o.Progress != nil {
				o.Progress(r.Name)
			}
			before := s.repoHolds(r.Name, host, st.ServerName)
			rs, err := s.repoSync(ctx, host, r.Name, false)
			out.fill(err)
			if rs != nil && err == nil {
				for _, id := range rs.Snapshots {
					if !before[id] {
						out.Copied++
					}
				}
				if rs.Downloaded == 0 {
					out.State = CopyCurrent
				}
			}
		}
		res.Locations = append(res.Locations, out)
	}
	for _, c := range s.Config.Clouds {
		out := AutoCopyOutcome{Location: c.Name, Kind: "cloud"}
		if !wantedCloud(c.Name) {
			out.State = CopyOff
			res.Locations = append(res.Locations, out)
			continue
		}
		if o.Progress != nil {
			o.Progress(c.Name)
		}
		cr, err := s.syncCloud(ctx, host, c.Name, false)
		out.fill(err)
		if cr != nil && err == nil {
			out.Path, out.Copied = cr.Path, len(cr.Leaves)
		}
		res.Locations = append(res.Locations, out)
	}
	return res, nil
}

// repoHolds lists the snapshots a repository holds for the server.
func (s *Service) repoHolds(repo, host, serverName string) map[string]bool {
	out := map[string]bool{}
	for _, st := range s.RepoStatuses(host, serverName) {
		if st.Name != repo {
			continue
		}
		for _, ids := range st.Leaves {
			for _, id := range ids {
				out[id] = true
			}
		}
	}
	return out
}

// fill sets the state from a copy's error.
func (o *AutoCopyOutcome) fill(err error) {
	var busy *LocationBusyError
	switch {
	case err == nil:
		o.State = CopyCopied
	case errors.As(err, &busy):
		o.State, o.Reason = CopyBusy, "另一个 Valise 正在同步这个位置"
		o.Suggestion = "它结束后会是最新的；也可以稍后手动同步"
	default:
		o.State = CopyFailed
		o.Reason = err.Error()
		if ue, ok := valise.As(err); ok {
			o.Reason, o.Suggestion = ue.Reason, ue.Suggestion
		}
	}
}

func (s *Service) renewForOpen(ctx context.Context, host string, res *AutoCopyResult) {
	exp := s.ExpiringCerts(host)
	if len(exp) == 0 {
		return
	}
	done, err := s.RenewCerts(ctx, host)
	switch {
	case err != nil:
		res.CertError = errText1(err)
	case len(done) > 0:
		res.CertsRenewed = done
	default:
		res.CertsExpiring = exp
	}
}

// instancesOf lists the instances holding periods on the server.
func instancesOf(leaves []vaultrpc.LeafState) []string {
	var out []string
	for _, l := range leaves {
		if l.Leaf == vault.LegacyLeaf || l.Deleting {
			continue
		}
		inst, _, _ := strings.Cut(l.Leaf, "/")
		if inst != "" && !slices.Contains(out, inst) {
			out = append(out, inst)
		}
	}
	return out
}

// wantedRepos applies this client's bindings (docs/ARCHITECTURE.md 7.4):
// an instance with a binding for its current generation follows it; any
// other instance wants every repository. On open a repository is filled
// when one instance wants it; after a backup only that instance counts.
func (s *Service) wantedRepos(ctx context.Context, host string, trigger CopyTrigger, instance string, instances []string) func(string) bool {
	if trigger == TriggerBackup {
		instances = []string{instance}
	}
	all := func(string) bool { return true }
	if len(instances) == 0 || instances[0] == "" {
		return all
	}
	refs, err := s.agentRefs(ctx, host, instances)
	if err != nil {
		return all
	}
	return repoRule(ctx, trigger, instances, refs, NewProtectionBindingStore(s.Dirs.Data, s.Config.Repos))
}

// wantedClouds applies the same bindings to cloud drives. Unlike a repository,
// a cloud with no saved choice (or no binding at all) is wanted.
func (s *Service) wantedClouds(ctx context.Context, host string, trigger CopyTrigger, instance string, instances []string) func(string) bool {
	if trigger == TriggerBackup {
		instances = []string{instance}
	}
	all := func(string) bool { return true }
	if len(instances) == 0 || instances[0] == "" {
		return all
	}
	refs, err := s.agentRefs(ctx, host, instances)
	if err != nil {
		return all
	}
	return cloudRule(ctx, trigger, instances, refs, s.bindingStore())
}

// cloudRule is repoRule's counterpart for clouds: a location is wanted when
// any considered instance has no choice for it, or its choice says so.
func cloudRule(ctx context.Context, trigger CopyTrigger, instances []string, refs map[string]identity.AgentRef, store *ProtectionBindingStore) func(string) bool {
	all := func(string) bool { return true }
	var bindings []*protection.ClientBinding
	for _, inst := range instances {
		ref, ok := refs[inst]
		if !ok {
			return all
		}
		b, err := store.Read(ctx, ref)
		if err != nil || b == nil {
			return all
		}
		bindings = append(bindings, b)
	}
	return func(name string) bool {
		for _, b := range bindings {
			found := false
			for _, c := range b.CloudCopies {
				if c.RepoID != name {
					continue
				}
				found = true
				on := c.OnClientOpen
				if trigger == TriggerBackup {
					on = c.AfterBackup
				}
				if on != nil && *on {
					return true
				}
			}
			if !found {
				return true
			}
		}
		return false
	}
}

// repoRule is the rule itself, over the instances' refs and the store.
func repoRule(ctx context.Context, trigger CopyTrigger, instances []string, refs map[string]identity.AgentRef, store *ProtectionBindingStore) func(string) bool {
	all := func(string) bool { return true }
	want := map[string]bool{}
	for _, inst := range instances {
		ref, ok := refs[inst]
		if !ok {
			return all
		}
		b, err := store.Read(ctx, ref)
		if err != nil || b == nil {
			return all
		}
		for _, c := range b.LocalCopies {
			on := c.OnClientOpen
			if trigger == TriggerBackup {
				on = c.AfterBackup
			}
			if on != nil && *on {
				want[c.RepoID] = true
			}
		}
	}
	return func(repo string) bool { return want[repo] }
}

// agentRefs reads the instances' current AgentRefs on one session;
// unbound instances are left out.
func (s *Service) agentRefs(ctx context.Context, host string, instances []string) (map[string]identity.AgentRef, error) {
	out := map[string]identity.AgentRef{}
	err := s.withSession(ctx, host, func(c caller) error {
		for _, inst := range instances {
			var b identity.Binding
			if err := c.Call(ctx, identity.MethodGet, identity.GetParams{InstanceID: inst}, &b); err != nil {
				return err
			}
			if b.IdentityState == identity.Bound && b.Agent != nil {
				out[inst] = b.Agent.Ref
			}
		}
		return nil
	})
	return out, err
}
