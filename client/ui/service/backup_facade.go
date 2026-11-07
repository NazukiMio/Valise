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
		c, err := NewBackupController(BackupControllerConfig{StateDir: s.Dirs.Data, RPC: s.call, Review: s.BackupReview, Capabilities: s.BackupCapabilities})
		if err != nil {
			return nil, err
		}
		s.backupController = c
	}
	return s.backupController, nil
}

// Tasks is the client's local background task runner (pull to a local
// repository and other work that must pass through this computer).
func (s *Service) Tasks() (*LocalTasks, error) {
	s.readersMu.Lock()
	defer s.readersMu.Unlock()
	if s.localTasks == nil {
		t, err := NewLocalTasks(s.Dirs.Data)
		if err != nil {
			return nil, err
		}
		s.localTasks = t
	}
	return s.localTasks, nil
}

// Shutdown waits for in-flight work to reach a safe boundary before a normal
// exit. A detached server run continues on the server.
func (s *Service) Shutdown(ctx context.Context) error {
	s.readersMu.Lock()
	b, l := s.backupController, s.localTasks
	s.readersMu.Unlock()
	var err error
	if b != nil {
		err = b.Shutdown(ctx)
	}
	if l != nil {
		if e := l.Shutdown(ctx); err == nil {
			err = e
		}
	}
	return err
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
