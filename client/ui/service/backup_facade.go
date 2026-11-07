package service

import (
	"context"

	"valise/core/identity"
)

// backupService is the planned-backup executor. The legacy Seal entry keeps its
// explicit tier semantics and is not routed through here.
func (s *Service) backupService() (*BackupController, error) {
	s.readersMu.Lock()
	defer s.readersMu.Unlock()
	if s.backupController == nil {
		c, err := NewBackupController(BackupControllerConfig{StateDir: s.Dirs.Data, RPC: s.call, Review: s.BackupReview})
		if err != nil {
			return nil, err
		}
		s.backupController = c
	}
	return s.backupController, nil
}

func (s *Service) PrepareBackup(ctx context.Context, host string, ref identity.AgentRef) (BackupPlan, error) {
	c, err := s.backupService()
	if err != nil {
		return BackupPlan{}, err
	}
	return c.PrepareBackup(ctx, host, ref)
}

func (s *Service) StartBackup(ctx context.Context, planID, submissionKey string) (BackupTask, error) {
	c, err := s.backupService()
	if err != nil {
		return BackupTask{}, err
	}
	return c.StartBackup(ctx, planID, submissionKey)
}

func (s *Service) GetBackupTask(ctx context.Context, ref ProtectionTaskRef) (BackupTask, error) {
	c, err := s.backupService()
	if err != nil {
		return BackupTask{}, err
	}
	return c.GetBackupTask(ctx, ref)
}
