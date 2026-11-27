package service

import (
	"context"
	"slices"
	"strings"

	"valise/core/enroll"
	"valise/core/login"
	"valise/core/profile"
	"valise/core/valise"
)

func (s *Service) resolveEntry(ctx context.Context, host, instance, entryID string) (*LoginTarget, *profile.Entry, error) {
	target, err := s.LoginMethods(ctx, host, instance)
	if err != nil {
		return nil, nil, err
	}
	for i := range target.Profile.Entries {
		if entryID == "" || target.Profile.Entries[i].ID == entryID {
			return target, &target.Profile.Entries[i], nil
		}
	}
	if entryID == "" {
		return nil, nil, valise.New("entry_none", "harness 资料 "+target.Profile.ID+" 没有声明入口", "这个 harness 没有本机 Web 页面")
	}
	return nil, nil, valise.New("entry_unknown", "harness 资料 "+target.Profile.ID+" 中没有入口 "+entryID, "检查入口 id 后重试")
}

// ProbeEntry probes an entry's listening, preparation, and Valise-managed
// runtime state. On older porters without entry-lifecycle/1, it gracefully
// falls back to listen_addrs so callers still get accurate listening state.
func (s *Service) ProbeEntry(ctx context.Context, host, instance, entryID string) (*login.EntryProbeResult, error) {
	_, entry, err := s.resolveEntry(ctx, host, instance, entryID)
	if err != nil {
		return nil, err
	}
	c, err := s.session(ctx, host)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	if c.hello == nil || !slices.Contains(c.hello.Capabilities, login.EntryLifecycleCapability) {
		var listen login.ListenResult
		if err := c.Call(ctx, login.MethodListen, login.ListenParams{Port: entry.Port}, &listen); err != nil {
			return nil, err
		}
		return &login.EntryProbeResult{
			EntryID:     entry.ID,
			Port:        entry.Port,
			Listening:   len(listen.Addrs) > 0,
			ListenAddrs: listen.Addrs,
			HasPrepare:  entry.Prepare != nil,
			Prepared:    entry.Prepare == nil,
			HasStart:    entry.Start != nil && len(entry.Start.Argv) > 0,
			AgentHint:   entry.AgentHint,
			LocalSource: entry.LocalSource,
		}, nil
	}
	var res login.EntryProbeResult
	if err := c.Call(ctx, login.MethodEntryProbe, login.EntryProbeParams{InstanceID: instance, Entry: *entry}, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// PrepareEntry runs an entry's declared prepare steps on host (TierAdmin).
func (s *Service) PrepareEntry(ctx context.Context, host, instance, entryID string) (*login.EntryPrepareResult, error) {
	stopOp := s.BeginOperation()
	defer stopOp()
	_, entry, err := s.resolveEntry(ctx, host, instance, entryID)
	if err != nil {
		return nil, err
	}
	if entry.Prepare == nil || len(entry.Prepare.Steps) == 0 {
		return nil, valise.New("entry_prepare_none", "该入口没有声明需要准备的步骤", "可以直接尝试启动或打开入口")
	}
	c, err := s.session(ctx, host)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	if c.hello == nil || !slices.Contains(c.hello.Capabilities, login.EntryLifecycleCapability) {
		return nil, valise.New("unsupported_capability", "服务器 porter 尚不支持入口按需准备（entry-lifecycle/1）", "先在服务器设置中更新 porter 后重试")
	}
	var res login.EntryPrepareResult
	if err := c.Call(ctx, login.MethodEntryPrepare, login.EntryPrepareParams{InstanceID: instance, Entry: *entry}, &res); err != nil {
		if rpcMissing(err) {
			return nil, valise.New("unsupported_capability", "服务器 porter 尚不支持入口按需准备（entry-lifecycle/1）", "先在服务器设置中更新 porter 后重试")
		}
		return nil, err
	}
	return &res, nil
}

// StartEntry starts an entry detached in the background on host (TierOwner).
func (s *Service) StartEntry(ctx context.Context, host, instance, entryID string) (*login.EntryStartResult, error) {
	stopOp := s.BeginOperation()
	defer stopOp()
	_, entry, err := s.resolveEntry(ctx, host, instance, entryID)
	if err != nil {
		return nil, err
	}
	if entry.Start == nil || len(entry.Start.Argv) == 0 {
		return nil, valise.New("entry_start_none", "该入口没有声明后台启动命令", "在服务器终端手动启动该入口后重试")
	}
	op, err := s.ownerPorter(ctx, host)
	if err != nil {
		return nil, err
	}
	if op.found != nil && !op.found.Supports(login.EntryLifecycleCapability) {
		return nil, valise.New("unsupported_capability", "服务器 porter 尚不支持入口按需启动（entry-lifecycle/1）", "先在服务器设置中更新 porter 后重试")
	}
	var res login.EntryStartResult
	if err := s.callOwner(ctx, op, nil, login.MethodEntryStart, login.EntryStartParams{InstanceID: instance, Entry: *entry}, &res); err != nil {
		if rpcMissing(err) {
			return nil, valise.New("unsupported_capability", "服务器 porter 尚不支持入口按需启动（entry-lifecycle/1）", "先在服务器设置中更新 porter 后重试")
		}
		return nil, err
	}
	return &res, nil
}

// StopEntry stops a Valise-started entry process or unit on host (TierOwner).
func (s *Service) StopEntry(ctx context.Context, host, instance, entryID string) (*login.EntryStopResult, error) {
	stopOp := s.BeginOperation()
	defer stopOp()
	_, entry, err := s.resolveEntry(ctx, host, instance, entryID)
	if err != nil {
		return nil, err
	}
	op, err := s.ownerPorter(ctx, host)
	if err != nil {
		return nil, err
	}
	if op.found != nil && !op.found.Supports(login.EntryLifecycleCapability) {
		return nil, valise.New("unsupported_capability", "服务器 porter 尚不支持停止远端入口（entry-lifecycle/1）", "先在服务器设置中更新 porter 后重试")
	}
	var res login.EntryStopResult
	if err := s.callOwner(ctx, op, nil, login.MethodEntryStop, login.EntryStopParams{InstanceID: instance, Entry: *entry}, &res); err != nil {
		if rpcMissing(err) {
			return nil, valise.New("unsupported_capability", "服务器 porter 尚不支持停止远端入口（entry-lifecycle/1）", "先在服务器设置中更新 porter 后重试")
		}
		return nil, err
	}
	return &res, nil
}

// RemoveLocalEntry removes an agent-reported or locally added web entry from
// instance on host (TierOwner) and invalidates cached agent observations.
func (s *Service) RemoveLocalEntry(ctx context.Context, host, instance, entryID string) error {
	entryID = strings.TrimSpace(entryID)
	if entryID == "" {
		return valise.New("request_invalid", "缺少入口标识", "选择要移除的本地入口")
	}
	_, err := s.UpdateDossier(ctx, host, enroll.DossierUpdateParams{
		InstanceID:     instance,
		RemoveEntryIDs: []string{entryID},
	})
	return err
}
