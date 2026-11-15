package gui

import (
	"context"
	"time"

	"valise/ui/service"
)

func (a *App) showBackupTask(br *backupRun, task service.BackupTask) {
	a.update(func() {
		if br.task == nil || task.Revision >= br.task.Revision {
			br.task = &task
		}
	})
}

// Start can remain in the SSH connection for minutes. Read its durable
// task by plan ID while waiting; only the one Start call can modify data.
func (a *App) followStartedBackup(ctx context.Context, br *backupRun, planID string) error {
	type result struct {
		task service.BackupTask
		err  error
	}
	started := make(chan result, 1)
	go func() {
		t, err := a.svc.StartBackup(ctx, planID, "desktop-"+planID)
		started <- result{t, err}
	}()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var task service.BackupTask
	returned := false
	for {
		select {
		case r := <-started:
			returned = true
			if r.task.TaskRef.TaskID != "" {
				task = r.task
				a.showBackupTask(br, task)
			}
			if r.err != nil {
				return r.err
			}
		case <-ticker.C:
			if task.TaskRef.TaskID == "" {
				tasks, err := a.svc.ListBackupTasks(ctx)
				if err == nil {
					for _, t := range tasks {
						if t.PlanID == planID {
							task = t
							break
						}
					}
				}
			}
			if task.TaskRef.TaskID != "" {
				queryCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
				t, err := a.svc.GetBackupTask(queryCtx, task.TaskRef)
				cancel()
				if err == nil {
					task = t
					a.showBackupTask(br, task)
				}
			}
		case <-ctx.Done():
			return ctx.Err()
		}
		if returned && !backupUnresolved(task.State) {
			if task.Error != nil {
				return taskErr(task.Error)
			}
			return nil
		}
		if returned && (task.State == "reconciling" || task.State == "needs_recovery") {
			return nil // The card offers a query, never another Start.
		}
	}
}

// reconcileBackup queries the original run once. A known live run is
// followed, including an attached run owned by another client process.
func (a *App) reconcileBackup(br *backupRun) {
	if br.j.busy || br.task == nil {
		return
	}
	ref := br.task.TaskRef
	a.start(&br.j, 6*time.Hour, func(ctx context.Context) error {
		a.says(&br.j)(T("backup.checking"))
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			t, err := a.svc.GetBackupTask(ctx, ref)
			if err != nil {
				return err
			}
			a.showBackupTask(br, t)
			if t.State != "running" && t.State != "cancelling" {
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
			}
		}
	}, nil)
}

// restoreBackupCards is local index discovery, scoped to the exact Agent
// generation. Keep all unresolved tasks plus the latest finished task.
func (a *App) restoreBackupCards(ps *protState, host string, tasks []service.BackupTask) {
	if ps.ref == nil {
		return
	}
	known := map[string]bool{}
	if ps.backup != nil && ps.backup.task != nil {
		known[ps.backup.task.TaskRef.TaskID] = true
	}
	for _, br := range ps.otherBackups {
		known[br.task.TaskRef.TaskID] = true
	}
	for _, t := range tasks {
		if t.Host != host || t.AgentRef != *ps.ref || known[t.TaskRef.TaskID] || t.State == "waiting" {
			continue
		}
		br := &backupRun{task: &t}
		if ps.backup == nil {
			ps.backup = br
		} else if backupUnresolved(t.State) {
			ps.otherBackups = append(ps.otherBackups, br)
		}
	}
}
