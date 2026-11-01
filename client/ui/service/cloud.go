package service

import (
	"context"
	"os/exec"
	"sort"

	"valise/core/login"
	"valise/core/vault"
	"valise/core/vaultrpc"
)

// Vault tree and cloud backup (docs/VAULT_FORMAT.md section 10,
// docs/ARCHITECTURE.md section 7).

func (s *Service) call(ctx context.Context, host, method string, params, result any) error {
	return s.withSession(ctx, host, func(c caller) error {
		return c.Call(ctx, method, params, result)
	})
}

// ServerNameSet names a server once; the name is its cloud backup folder.
func (s *Service) ServerNameSet(ctx context.Context, host, name string) error {
	return s.call(ctx, host, vaultrpc.MethodServerNameSet, vaultrpc.ServerNameSetParams{Name: name}, nil)
}

// PeriodSet sets an instance's period; it applies from the next period.
func (s *Service) PeriodSet(ctx context.Context, host, instance string, setting vault.PeriodSetting) error {
	return s.call(ctx, host, vaultrpc.MethodPeriodSet, vaultrpc.PeriodSetParams{InstanceID: instance, Setting: setting}, nil)
}

// LeafDelete deletes one whole period on the server.
func (s *Service) LeafDelete(ctx context.Context, host, leaf string) (*vaultrpc.LeafDeleteResult, error) {
	var res vaultrpc.LeafDeleteResult
	err := s.call(ctx, host, vaultrpc.MethodLeafDelete, vaultrpc.LeafDeleteParams{Leaf: leaf}, &res)
	return &res, err
}

// TargetList lists what a cloud backup holds for host.
func (s *Service) TargetList(ctx context.Context, host, target string) (*vaultrpc.TargetListResult, error) {
	var res vaultrpc.TargetListResult
	err := s.call(ctx, host, vaultrpc.MethodTargetList, vaultrpc.TargetListParams{Target: target}, &res)
	return &res, err
}

// TargetLeafDelete deletes one whole period on a cloud backup.
func (s *Service) TargetLeafDelete(ctx context.Context, host, target, leaf string) error {
	return s.call(ctx, host, vaultrpc.MethodTargetLeafDel, vaultrpc.TargetLeafDeleteParams{Target: target, Leaf: leaf}, nil)
}

// TimerSet turns an instance's timed seal on or off.
func (s *Service) TimerSet(ctx context.Context, host string, p vaultrpc.TimerSetParams) (*vaultrpc.TimerStatus, error) {
	var res vaultrpc.TimerStatus
	err := s.call(ctx, host, vaultrpc.MethodTimerSet, p, &res)
	return &res, err
}

// SealLog lists who started each of an instance's snapshots and why.
func (s *Service) SealLog(ctx context.Context, host, instance string) (*vaultrpc.SealLogResult, error) {
	var res vaultrpc.SealLogResult
	err := s.call(ctx, host, vaultrpc.MethodSealLog, vaultrpc.SealLogParams{InstanceID: instance}, &res)
	return &res, err
}

// TimerStatus reads an instance's timed seal.
func (s *Service) TimerStatus(ctx context.Context, host, instance string) (*vaultrpc.TimerStatus, error) {
	var res vaultrpc.TimerStatus
	err := s.call(ctx, host, vaultrpc.MethodTimerStatus, vaultrpc.TimerStatusParams{InstanceID: instance}, &res)
	return &res, err
}

// Copy is where else a leaf exists, and whether that copy is complete.
type Copy struct {
	// Where is "server" or a target name.
	Where    string `json:"where"`
	Complete bool   `json:"complete"`
	Deleting bool   `json:"deleting,omitempty"`
}

// LeafCopies lists every place a leaf of host exists: the server and each
// cloud backup. A copy is complete when it holds the same snapshots and
// packs as the most complete copy (docs/VAULT_FORMAT.md 10.4). Targets that
// cannot be listed are left out and reported in errs.
func (s *Service) LeafCopies(ctx context.Context, host, leaf string) (copies []Copy, errs map[string]error, err error) {
	st, err := s.VaultStatus(ctx, host)
	if err != nil {
		return nil, nil, err
	}
	type set struct {
		snaps, packs map[string]bool
		deleting     bool
	}
	found := map[string]set{}
	for _, l := range st.Leaves {
		if l.Leaf == leaf {
			sn := map[string]bool{}
			for _, id := range l.Snapshots {
				sn[id] = true
			}
			found["server"] = set{snaps: sn, deleting: l.Deleting}
		}
	}
	errs = map[string]error{}
	for _, t := range st.Targets {
		list, err := s.TargetList(ctx, host, t.Name)
		if err != nil {
			errs[t.Name] = err
			continue
		}
		for _, l := range list.Leaves {
			if l.Leaf != leaf {
				continue
			}
			sn, pk := map[string]bool{}, map[string]bool{}
			for _, id := range l.Snapshots {
				sn[id] = true
			}
			for _, id := range l.Packs {
				pk[id] = true
			}
			found[t.Name] = set{snaps: sn, packs: pk, deleting: l.Deleting}
		}
	}
	best := 0
	for _, f := range found {
		if len(f.snaps) > best {
			best = len(f.snaps)
		}
	}
	for where, f := range found {
		copies = append(copies, Copy{Where: where, Complete: !f.deleting && len(f.snaps) == best && best > 0, Deleting: f.deleting})
	}
	sort.Slice(copies, func(i, j int) bool { return copies[i].Where < copies[j].Where })
	return copies, errs, nil
}

// ShellCommand returns an unstarted interactive login shell on host over
// a fresh admin connection (docs/INSTALL.md section 2); call done after it
// exits.
func (s *Service) ShellCommand(ctx context.Context, host string) (cmd *exec.Cmd, done func(), err error) {
	t, done, err := s.adminTransport(ctx, host)
	if err != nil {
		return nil, nil, err
	}
	return exec.CommandContext(ctx, t.Binary(), t.ShellArgs()...), done, nil
}

// entryAuth asks porter for the URL suffix holding an entry's token.
func entryAuth(ctx context.Context, c caller, p login.EntryAuthParams) (string, error) {
	var res login.EntryAuthResult
	if err := c.Call(ctx, login.MethodEntryAuth, p, &res); err != nil {
		return "", err
	}
	return res.Suffix, nil
}
