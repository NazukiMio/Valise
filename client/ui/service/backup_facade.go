package service

import (
	"context"

	"valise/client/config"
	"valise/core/identity"
)

// backupService is the planned-backup executor. The legacy Seal entry keeps its
// explicit tier semantics and is not routed through here.
func (s *Service) backupService() (*BackupController, error) {
	s.readersMu.Lock()
	defer s.readersMu.Unlock()
	if s.backupController == nil {
		c, err := NewBackupController(BackupControllerConfig{StateDir: s.Dirs.Data, RPC: s.call, Review: s.BackupReview, Capabilities: s.detachCapabilities})
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
	t, err := c.StartBackup(ctx, planID, submissionKey)
	return s.withDetachHint(t), err
}

func (s *Service) GetBackupTask(ctx context.Context, ref ProtectionTaskRef) (BackupTask, error) {
	c, err := s.backupService()
	if err != nil {
		return BackupTask{}, err
	}
	t, err := c.GetBackupTask(ctx, ref)
	return s.withDetachHint(t), err
}

// DetachHint is shown with a backup that ran in the connection because
// 分离模式 is off.
const DetachHint = "任务也可以在后台跑，查看「开启分离模式」"

// detachCapabilities hands the backup controller the server's capabilities
// only with 分离模式 on, so it asks for detached execution only then
// (N2 增补决定 F4); otherwise backups stay in the connection.
func (s *Service) detachCapabilities(ctx context.Context, host string) ([]string, error) {
	if !s.Config.DetachedTasks {
		return nil, nil
	}
	return s.BackupCapabilities(ctx, host)
}

func (s *Service) withDetachHint(t BackupTask) BackupTask {
	if t.TaskRef.TaskID != "" && !t.Async && !s.Config.DetachedTasks {
		t.Hint = DetachHint
	}
	return t
}

// SetDetachedTasks turns 分离模式 on or off for this client.
func (s *Service) SetDetachedTasks(on bool) error {
	if err := config.SetDetachedTasks(s.Dirs.Data, on); err != nil {
		return err
	}
	s.Config.DetachedTasks = on
	return nil
}
