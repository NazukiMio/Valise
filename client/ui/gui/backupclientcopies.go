package gui

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"
	"valise/ui/service"
)

// The server run and this computer's copies are separate facts. Reuse the
// same AutoCopy policy as the CLI/TUI, only after a committed backup.
func (a *App) copyAfterBackup(ctx context.Context, br *backupRun, planID, host, instance string) error {
	if len(a.svc.Config.Repos) == 0 && len(a.svc.Config.Clouds) == 0 {
		return nil
	}
	tasks, err := a.svc.ListBackupTasks(ctx)
	if err != nil {
		return err
	}
	for _, t := range tasks {
		if t.PlanID != planID || t.Run == nil || t.Run.Snapshot == nil || t.State != "succeeded" && t.State != "partial" {
			continue
		}
		a.update(func() { br.localBusy = true; br.localAt = time.Now() })
		say := a.says(&br.j)
		say(T("backup.local_start"))
		res, err := a.svc.AutoCopy(ctx, host, service.TriggerBackup, service.AutoCopyOptions{Instance: instance, Progress: func(name string) { say(T("backup.local_copying", "name", name)) }})
		a.update(func() { br.localBusy = false; br.localEnd = time.Now(); br.localCopies = res })
		return err
	}
	return nil
}

func (a *App) clientCopyLines(c *ui.Context, br *backupRun) {
	if br.localBusy {
		ui.Row(c).Gap(8).Children(func() { ui.Spinner(c); muted(ui.Text(c, a.doingText(br.j.doing, br.j.doingAt, c.Now()))).Wrap() })
		muted(ui.Text(c, T("backup.local_keep_open"))).Wrap()
	}
	if !br.localAt.IsZero() {
		end := br.localEnd
		if br.localBusy {
			end = c.Now()
		}
		if !end.IsZero() {
			muted(ui.Text(c, T("backup.local_clock", "duration", backupDuration(end.Sub(br.localAt))))).Wrap()
		}
	}
	if br.localCopies == nil {
		return
	}
	for _, loc := range br.localCopies.Locations {
		state := T("backup.local_state.off")
		switch loc.State {
		case service.CopyCopied:
			state = T("backup.local_state.copied")
		case service.CopyCurrent:
			state = T("backup.local_state.current")
		case service.CopyFailed, service.CopyUnmounted, service.CopyBusy:
			state = T("backup.local_state.failed")
		}
		ui.Text(c, T("backup.local_result", "name", loc.Location, "state", state)).Wrap()
		if loc.Reason != "" {
			muted(ui.Text(c, loc.Reason)).Wrap()
			muted(ui.Text(c, loc.Suggestion)).Wrap()
		}
	}
}
