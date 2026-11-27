package service

import (
	"context"

	"valise/core/protection"
	"valise/core/valise"
	"valise/core/vault"
)

func (s *Service) protectionService() (*ProtectionController, error) {
	s.readersMu.Lock()
	defer s.readersMu.Unlock()
	if s.protectionController == nil {
		c, err := NewProtectionController(ProtectionControllerConfig{
			StateDir: s.Dirs.Data,
			RPC:      s.call,
			Review:   s.ProtectionReview,
		})
		if err != nil {
			return nil, err
		}
		s.protectionController = c
	}
	return s.protectionController, nil
}
func (s *Service) GetProtection(ctx context.Context, host string, p protection.GetParams) (protection.View, error) {
	c, err := s.protectionService()
	if err != nil {
		return protection.View{}, err
	}
	return c.GetProtection(ctx, host, p)
}
func (s *Service) PrepareProtectionChange(ctx context.Context, host string, p protection.PrepareParams) (protection.FrozenChange, error) {
	if p.Desired.Scope.Tier == vault.TierSoul && s.instanceLacksSecretRules(ctx, host, p.AgentRef.InstanceID) {
		return protection.FrozenChange{}, noSecretRulesError()
	}
	c, err := s.protectionService()
	if err != nil {
		return protection.FrozenChange{}, err
	}
	return c.PrepareProtectionChange(ctx, host, p)
}

func noSecretRulesError() error {
	return valise.New("no_secret_rules_declared",
		"该实例尚未声明凭据位置，暂不能使用“轻装（剥离凭据）”档位",
		"请使用“完整”或“标准”加密档位，或先通过 Agent 归档建议确认凭据文件后再使用轻装档位")
}

func (s *Service) instanceLacksSecretRules(ctx context.Context, host, instance string) bool {
	if s == nil || host == "" || instance == "" || s.Config.SSHBinary == "" {
		return false
	}
	insts, err := s.Instances(ctx, host)
	if err != nil {
		return false
	}
	for _, in := range insts {
		if in.InstanceID == instance {
			return in.HasSecretRules != nil && !*in.HasSecretRules
		}
	}
	return false
}
func (s *Service) StartProtectionChange(ctx context.Context, planID, submissionKey string) (ProtectionTask, error) {
	stopOp := s.BeginOperation()
	defer stopOp()
	c, err := s.protectionService()
	if err != nil {
		return ProtectionTask{}, err
	}
	return c.StartProtectionChange(ctx, planID, submissionKey)
}
func (s *Service) GetProtectionTask(ctx context.Context, ref ProtectionTaskRef) (ProtectionTask, error) {
	c, err := s.protectionService()
	if err != nil {
		return ProtectionTask{}, err
	}
	return c.GetTask(ctx, ref)
}
