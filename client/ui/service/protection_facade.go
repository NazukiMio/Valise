package service

import (
	"context"

	"valise/core/protection"
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
	c, err := s.protectionService()
	if err != nil {
		return protection.FrozenChange{}, err
	}
	return c.PrepareProtectionChange(ctx, host, p)
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
