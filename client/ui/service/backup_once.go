package service

import (
	"context"
	"slices"

	"valise/core/identity"
	"valise/core/protection"
	"valise/core/valise"
)

func (s *Service) allowOneOffBackup(ctx context.Context, host string) error {
	caps, err := s.BackupCapabilities(ctx, host)
	if err != nil {
		return err
	}
	if !slices.Contains(caps, protection.BackupCapabilityV3) {
		return valise.New("unsupported_capability", "这个 porter 版本不支持一次性备份选择", "先更新服务器上的 porter，再选择本次范围与是否复制")
	}
	return nil
}

func (c *BackupController) allowOneOff(ctx context.Context, host string) error {
	if c.config.AllowOneOff == nil {
		return valise.New("unsupported_capability", "尚未核实服务器的一次性备份能力", "更新 porter 并重新准备本次备份")
	}
	return c.config.AllowOneOff(ctx, host)
}

func (s *Service) PrepareOneOffBackup(ctx context.Context, host string, ref identity.AgentRef, selection protection.BackupSelection) (BackupPlan, error) {
	c, err := s.backupService()
	if err != nil {
		return BackupPlan{}, err
	}
	if selection.Tier == "" {
		return BackupPlan{}, valise.New("unsupported_scope", "没有选择本次范围", "选择一个现有备份档位")
	}
	if err := selection.Validate(); err != nil {
		return BackupPlan{}, err
	}
	if err := c.allowOneOff(ctx, host); err != nil {
		return BackupPlan{}, err
	}
	p, err := c.PrepareBackup(ctx, host, ref)
	if err != nil {
		return p, err
	}
	p.Selection = selection
	p.IncludesCredentials = selection.Tier != "soul"
	p.PlanDigest, _ = protection.Digest(p)
	c.mu.Lock()
	c.plans[p.PlanID] = backupPrepared{host: host, plan: cloneProtection(p)}
	c.mu.Unlock()
	return p, nil
}
