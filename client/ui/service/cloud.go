package service

import (
	"context"
	"os/exec"

	"valise/client/porter"
	"valise/core/login"
	"valise/core/valise"
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
	if vault.IsSavepointLeaf(leaf) {
		return nil, permanentLeaf()
	}
	var res vaultrpc.LeafDeleteResult
	err := s.call(ctx, host, vaultrpc.MethodLeafDelete, vaultrpc.LeafDeleteParams{Leaf: leaf}, &res)
	return &res, err
}

// TargetList lists what a cloud backup holds for host. A managed station
// is listed from this computer: the server's certificate may only write.
func (s *Service) TargetList(ctx context.Context, host, target string) (*vaultrpc.TargetListResult, error) {
	if s.IsStation(target) {
		return s.StationList(ctx, host, target)
	}
	var res vaultrpc.TargetListResult
	err := s.call(ctx, host, vaultrpc.MethodTargetList, vaultrpc.TargetListParams{Target: target}, &res)
	return &res, err
}

// TargetLeafDelete deletes one whole period on a cloud backup.
func (s *Service) TargetLeafDelete(ctx context.Context, host, target, leaf string) error {
	if vault.IsSavepointLeaf(leaf) {
		return permanentLeaf()
	}
	if s.IsStation(target) {
		return s.stationLeafDelete(ctx, host, target, leaf)
	}
	return s.call(ctx, host, vaultrpc.MethodTargetLeafDel, vaultrpc.TargetLeafDeleteParams{Target: target, Leaf: leaf}, nil)
}

func permanentLeaf() error {
	return valise.New("leaf_permanent", "手动存档点永久保留，不能整组删除", "选择已结束的日期周期")
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

// ShellCommand returns an unstarted interactive login shell on host over
// a fresh admin connection (docs/INSTALL.md section 2); with profileID it
// is the harness terminal instead. Call done after it exits.
func (s *Service) ShellCommand(ctx context.Context, host, profileID string) (cmd *exec.Cmd, done func(), err error) {
	var dirs []string
	if profileID != "" {
		set, err := s.Profiles()
		if err != nil {
			return nil, nil, err
		}
		lp := set.ByID[profileID]
		if lp == nil {
			return nil, nil, valise.New("profile_missing", "本机没有 harness 资料 "+profileID, "安装或更新 harness 资料包：valise profiles install")
		}
		if lp.Effective.Detect != nil {
			dirs = lp.Effective.Detect.BinDirs
		}
	}
	if err := s.RequireGate(ctx, host, ""); err != nil {
		return nil, nil, err
	}
	t, done, err := s.adminTransport(ctx, host)
	if err != nil {
		return nil, nil, err
	}
	if profileID == "" {
		return exec.CommandContext(ctx, t.Binary(), t.ShellArgs()...), done, nil
	}
	// The harness terminal (docs/INSTALL.md section 2): the user's own
	// shell with the harness's bin dirs on PATH, nothing edited on the
	// server.
	found, err := porter.Probe(ctx, t)
	if err != nil {
		done()
		return nil, nil, err
	}
	return t.TTYCommand(ctx, execLine(found.Path, dirs, []string{"--shell"})), done, nil
}

// entryAuth asks porter for the URL suffix holding an entry's token.
func entryAuth(ctx context.Context, c caller, p login.EntryAuthParams) (string, error) {
	var res login.EntryAuthResult
	if err := c.Call(ctx, login.MethodEntryAuth, p, &res); err != nil {
		return "", err
	}
	return res.Suffix, nil
}
