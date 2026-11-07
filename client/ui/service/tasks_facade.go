package service

import (
	"context"
	"slices"

	"valise/core/valise"
)

// BackupCapabilities reads host's hello capabilities on a fresh daily
// session. It is the BackupControllerConfig.Capabilities adapter.
func (s *Service) BackupCapabilities(ctx context.Context, host string) ([]string, error) {
	var caps []string
	err := s.withSession(ctx, host, func(c caller) error {
		p, ok := c.(*porterSession)
		if !ok || p.hello == nil {
			return valise.New("unsupported_capability", "无法读取服务器 porter 的能力", "重新连接服务器后再试")
		}
		caps = slices.Clone(p.hello.Capabilities)
		return nil
	})
	return caps, err
}

func (s *Service) ListBackupTasks(ctx context.Context) ([]BackupTask, error) {
	c, err := s.backupService()
	if err != nil {
		return nil, err
	}
	return c.ListBackupTasks(ctx)
}

func (s *Service) WatchBackupTask(ctx context.Context, ref ProtectionTaskRef) (<-chan BackupTask, func(), error) {
	c, err := s.backupService()
	if err != nil {
		return nil, nil, err
	}
	return c.WatchBackupTask(ctx, ref)
}

func (s *Service) CancelBackupTask(ctx context.Context, ref ProtectionTaskRef) (BackupTask, error) {
	c, err := s.backupService()
	if err != nil {
		return BackupTask{}, err
	}
	return c.CancelBackupTask(ctx, ref)
}

// RepoSyncSummary is the redacted result kept in the local task index: counts
// only, no file names or problem locations.
type RepoSyncSummary struct {
	Repo       string `json:"repo"`
	Leaves     int    `json:"leaves"`
	Snapshots  int    `json:"snapshots"`
	Downloaded int    `json:"downloaded"`
	Bytes      int64  `json:"bytes"`
	Problems   int    `json:"problems"`
}

// StartRepoSyncTask runs the existing RepoSync as a local background task.
// RepoSync itself is unchanged; it records the repository state as before.
func (s *Service) StartRepoSyncTask(tasks *LocalTasks, host, repo, submissionKey string) (LocalTask, error) {
	if tasks == nil || host == "" {
		return LocalTask{}, valise.New("request_invalid", "缺少本机任务服务或服务器", "重新打开 Valise 后再试")
	}
	if _, err := s.repo(repo); err != nil {
		return LocalTask{}, err
	}
	return tasks.Start(LocalTaskRepoSync, LocalTaskSubject{Host: host, Repo: repo}, submissionKey, func(ctx context.Context, stage func(string)) (any, error) {
		stage("pulling")
		res, err := s.RepoSync(ctx, host, repo)
		if res == nil {
			return nil, err
		}
		return RepoSyncSummary{Repo: res.Repo, Leaves: len(res.Leaves), Snapshots: len(res.Snapshots), Downloaded: res.Downloaded, Bytes: res.Bytes, Problems: len(res.Problems)}, err
	})
}
