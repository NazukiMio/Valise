package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"valise/client/config"
	"valise/core/enroll"
	"valise/core/environment"
	"valise/core/identity"
	"valise/core/protection"
	"valise/core/revive"
)

// Stamp compares target facts, not changing Agent file content. Dynamic space
// is checked separately. The same checks run before staging and before swap.
func (s *Service) restoreStamp(ctx context.Context, p *PreparedRestore, ownOp string) (string, error) {
	rp := p.Revive
	var settings *environment.Settings
	if rp.Plan.Mode == revive.ModeRollback {
		v, err := s.InstanceEnvironmentSettings(ctx, rp.Target, rp.Plan.InstanceID)
		if err != nil {
			return "", err
		}
		if err := requireDirectRestore(v); err != nil {
			return "", err
		}
		settings = &v
	}
	var provenance enroll.Provenance
	if rp.Plan.Record != nil {
		provenance = rp.Plan.Record.Provenance
	} else if rp.targetFacts.Record != nil {
		provenance = rp.targetFacts.Record.Provenance
	}
	var facts revive.InspectResult
	o, err := s.ownerPorter(ctx, rp.Target)
	if err != nil {
		return "", err
	}
	err = s.callOwner(ctx, o, nil, revive.MethodInspect, revive.InspectParams{Profile: rp.Plan.Profile, InstanceID: rp.Plan.InstanceID, Roots: rp.Plan.Roots, Provenance: provenance, PlainBytes: rp.Bytes}, &facts)
	if err != nil {
		return "", err
	}
	for _, pending := range facts.PendingOps {
		if pending != ownOp {
			return "", restoreError("resource_busy", "目标存在另一项未完成操作")
		}
	}
	if len(facts.Busy) > 0 {
		return "", restoreError("root_busy", "有其他进程正在使用目标目录")
	}
	need := rp.NeedBytes
	if ownOp != "" {
		need = max(0, need-rp.Bytes)
	}
	for i := range facts.Roots {
		if facts.Roots[i].FreeBytes < uint64(need) {
			return "", restoreError("space_low", "目标剩余空间不足以准备材料并保留旧目录")
		}
		facts.Roots[i].FreeBytes, facts.Roots[i].UsedBytes = 0, 0
	}
	facts.PendingOps = nil
	facts.Busy = nil
	var binding identity.Binding
	if slices.Contains(rp.targetCaps, identity.Capability) {
		if err = s.call(ctx, rp.Target, identity.MethodGet, identity.GetParams{InstanceID: rp.Plan.InstanceID}, &binding); err != nil {
			return "", err
		}
	}
	var source identity.Binding
	if rp.Plan.Mode == revive.ModeMigrate && rp.sourceStatus {
		if err = s.call(ctx, rp.From, identity.MethodGet, identity.GetParams{InstanceID: rp.Snapshot.InstanceID}, &source); err != nil {
			return "", err
		}
		if rp.sourceAgent != nil && (source.Agent == nil || source.Agent.Ref != *rp.sourceAgent) {
			return "", restoreError("source_conflict", "原实例世代已变化，不能按快照的同名实例停用")
		}
	}
	trust, err := s.restoreTrust(rp.Target)
	if err != nil {
		return "", err
	}

	return protection.Digest(struct {
		Facts    revive.InspectResult
		Identity identity.Binding
		Source   identity.Binding
		Trust    string
		Settings *environment.Settings
	}{facts, binding, source, trust, settings})
}

func (s *Service) validateRestore(ctx context.Context, p *PreparedRestore, ownOp string) error {
	if ownOp == "" && time.Now().After(p.Expires) {
		return restoreError("plan_expired", "恢复计划已过期，尚未开始暂存")
	}
	stamp, err := s.restoreStamp(ctx, p, ownOp)
	if err != nil {
		return err
	}
	if stamp != p.stamp {
		return restoreError("plan_stale", "目标身份、目录、共享服务或来源条件已变化，旧确认已失效")
	}
	if p.Revive.ProfileFromSnapshot {
		return nil
	}
	set, err := s.Profiles()
	if err != nil {
		return err
	}
	prof := set.ByID[p.Revive.Plan.Profile.ID]
	if prof == nil {
		return restoreError("plan_stale", "准备计划时的资料已不存在")
	}
	current := prof.Effective.ForAgent(p.Revive.profileName)
	a, _ := json.Marshal(current)
	b, _ := json.Marshal(p.Revive.Plan.Profile)
	if string(a) != string(b) {
		return restoreError("plan_stale", "恢复资料或命令已改变")
	}
	return nil
}

func (s *Service) restoreTrust(host string) (string, error) {
	_, hc, err := s.transport(host)
	if err != nil {
		return "", err
	}
	var trusted []string
	for _, file := range []string{filepath.Join(s.Dirs.Data, config.KnownHostsFile), filepath.Join(s.Home, ".ssh", "known_hosts")} {
		b, e := os.ReadFile(file)
		if e != nil && !os.IsNotExist(e) {
			return "", e
		}
		for _, line := range strings.Split(string(b), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 3 {
				continue
			}
			if strings.HasPrefix(fields[0], "|") || strings.Contains(fields[0], host) || hc.HostName != "" && strings.Contains(fields[0], hc.HostName) {
				trusted = append(trusted, line)
			}
		}
	}
	return protection.Digest(struct {
		Server     config.Server
		Connection any
		Keys       []string
	}{s.server(host), hc, trusted})
}
