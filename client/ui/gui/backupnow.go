package gui

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/protection"
	"valise/core/valise"
	"valise/ui/service"
)

// “现在备份” (docs/UI.md 9.5): a backup of the plan in force, each part
// said apart: written to the server's backup library, copied elsewhere.

type backupRun struct {
	j                 job
	task              *service.BackupTask
	started, ended    time.Time
	hidden            bool
	cancelJ           job
	localCopies       *service.AutoCopyResult
	localBusy         bool
	localAt, localEnd time.Time
}

// confirm records that the person confirmed planID in this window; the
// service's review hooks ask confirmed before anything runs.
func (a *App) confirm(planID string) {
	a.confirms.Lock()
	defer a.confirms.Unlock()
	if a.confirms.ids == nil {
		a.confirms.ids = map[string]bool{}
	}
	a.confirms.ids[planID] = true
}

// confirmed takes a plan's confirmation once.
func (a *App) confirmed(planID string) bool {
	a.confirms.Lock()
	defer a.confirms.Unlock()
	ok := a.confirms.ids[planID]
	delete(a.confirms.ids, planID)
	return ok
}

// installReviews makes the window the person who confirms plan changes and
// backups: only a plan confirmed here goes on.
func (a *App) installReviews() {
	a.svc.BackupReview = func(_ context.Context, _ string, p service.BackupPlan) (bool, error) {
		return a.confirmed(p.PlanID), nil
	}
	a.svc.ProtectionReview = func(_ context.Context, _ string, p protection.FrozenChange) (bool, error) {
		return a.confirmed(p.PlanID), nil
	}
}

func taskErr(e *service.ProtectionTaskError) error {
	return valise.New(e.Code, e.Reason, e.Suggestion)
}

// startBackupNow makes sure the key exists and was backed up (the first
// time), then registers it on the server if needed and backs up.
func (a *App) startBackupNow(row *agentRow, ps *protState) {
	if ps.savepointsSupported {
		ps.oneOff = &oneOffBackup{planned: true}
		return
	}
	a.needKey(func() { a.runSelectedBackup(row, ps, protection.BackupSelection{}) })
}

func (a *App) runSelectedBackup(row *agentRow, ps *protState, selection protection.BackupSelection) {
	if ps.backup != nil && ps.backup.task != nil && backupUnresolved(ps.backup.task.State) {
		ps.otherBackups = append(ps.otherBackups, ps.backup)
	}
	br := &backupRun{started: time.Now()}
	ps.backup = br
	host, ref := row.Server, *ps.ref
	a.start(&br.j, 6*time.Hour, func(ctx context.Context) error {
		say := a.says(&br.j)
		say(T("backup.registering"))
		if err := a.ensureRegistered(ctx, host); err != nil {
			return err
		}
		say(T("backup.preparing"))
		var plan service.BackupPlan
		var err error
		if selection.Tier != "" || selection.Note != "" {
			plan, err = a.svc.PrepareOneOffBackup(ctx, host, ref, selection)
		} else {
			plan, err = a.svc.PrepareBackup(ctx, host, ref)
		}
		if err != nil {
			return err
		}
		a.confirm(plan.PlanID)
		say(T("backup.starting"))
		if err := a.followStartedBackup(ctx, br, plan.PlanID); err != nil {
			return err
		}
		if !selection.SkipCopies {
			return a.copyAfterBackup(ctx, br, plan.PlanID, host, row.Instance)
		}
		return nil
	}, func(error) { br.ended = time.Now(); a.readProt(host, ps); a.refreshTimeline() })
}

// ensureRegistered registers this computer's key on host before the first
// backup (docs/ARCHITECTURE.md 3 step 6).
func (a *App) ensureRegistered(ctx context.Context, host string) error {
	key, err := a.svc.KeyShow()
	if err != nil {
		return err
	}
	st, err := a.svc.VaultStatus(ctx, host)
	if err != nil {
		return err
	}
	for _, r := range st.Recipients {
		if r == key.Fingerprint {
			return nil
		}
	}
	name, _ := os.Hostname()
	_, _, err = a.svc.KeyRegister(ctx, host, strings.TrimSuffix(name, ".local"))
	return err
}

func (a *App) backupProgress(c *ui.Context, row *agentRow, ps *protState) {
	a.backupCard(c, row, ps.backup)
}

func (a *App) backupCard(c *ui.Context, row *agentRow, br *backupRun) {
	if br.hidden {
		return
	}
	if br.j.busy {
		c.After(time.Second)
	}
	card(ui.Column(c)).Gap(8).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, T("backup.title", "name", row.Name)).FontSize(16).Bold().Grow(1)
			if !br.j.busy && !br.cancelJ.busy && (br.task == nil || !backupUnresolved(br.task.State)) {
				ui.Button(c, T("common.close")).OnClick(func() { br.hidden = true })
			}
		})
		if text := backupClock(br, c.Now()); text != "" {
			if br.task != nil && br.task.Run != nil {
				text = T("backup.server_clock", "clock", text)
			}
			muted(ui.Text(c, text)).Wrap()
		}
		if br.task != nil {
			if br.task.Selection.Tier != "" {
				muted(ui.Text(c, T("backup.once.result", "tier", tierText(br.task.Selection.Tier)))).Wrap()
			}
			if br.task.Selection.Note != "" {
				fact(c, T("savepoint.note.label"), br.task.Selection.Note)
			}
			switch br.task.State {
			case "succeeded":
				ui.Text(c, T("backup.result.done")).Wrap()
			case "partial":
				ui.Text(c, T("backup.result.partial")).TextColor(palette.Warn).Wrap()
			case "cancelled":
				ui.Text(c, T("backup.result.cancelled")).Wrap()
			case "cancelling":
				ui.Text(c, T("backup.result.cancelling")).Wrap()
			}
		}
		var run *protection.BackupRun
		if br.task != nil {
			run = br.task.Run
		}
		if run == nil {
			if br.j.busy {
				if br.task == nil {
					a.jobLine(c, &br.j)
				} else {
					// Once accepted, stopping a local status query must not
					// pretend to cancel the server's execution.
					ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
						ui.Spinner(c)
						muted(ui.Text(c, a.doingText(br.j.doing, br.j.doingAt, c.Now()))).Wrap()
					})
				}
			}
		} else {
			for i, comp := range run.Components {
				backupComponentLine(c.Key(strconv.Itoa(i)), comp, c.Now(), backupIsRunning(br))
			}
			if backupIsRunning(br) {
				ui.Row(c).Justify(ui.End).Children(func() {
					if !br.cancelJ.busy {
						ui.Button(c, T("common.stop")).OnClick(func() { a.cancelBackup(br) })
					}
				})
			}
		}
		if br.j.err != nil {
			errorBox(c, br.j.err, a.fold("bk-err-"+row.Instance))
		} else if br.task != nil && br.task.Error != nil {
			errorBox(c, taskErr(br.task.Error), a.fold("bk-err-"+row.Instance))
		}
		if br.cancelJ.err != nil {
			errorBox(c, br.cancelJ.err, a.fold("bk-cancel-"+row.Instance))
		}
		a.clientCopyLines(c, br)
		if br.task != nil && br.task.Hint != "" && backupIsRunning(br) {
			muted(ui.Text(c, T("backup.detach_hint"))).Wrap()
		}
		if br.task != nil && !br.j.busy && backupUnresolved(br.task.State) {
			muted(ui.Text(c, T("backup.reconcile_hint"))).Wrap()
			ui.Button(c, T("backup.reconcile")).OnClick(func() { a.reconcileBackup(br) })
		}
	})
}

func (a *App) cancelBackup(br *backupRun) {
	if br.task == nil {
		a.stopJob(&br.j)
		return
	}
	ref := br.task.TaskRef
	a.start(&br.cancelJ, time.Minute, func(ctx context.Context) error {
		task, err := a.svc.CancelBackupTask(ctx, ref)
		if task.TaskRef.TaskID != "" {
			a.showBackupTask(br, task)
		}
		return err
	}, nil)
}

// missedCard says which scheduled backups were missed; 标记已读 is kept on
// this computer only, by the missed times (docs/UI.md 7.8).
func (a *App) missedCard(c *ui.Context, row *agentRow, ps *protState) {
	var times []string
	var caught int
	for _, m := range ps.view.MissedRuns {
		if m.State == "expired" {
			continue
		}
		if m.State == "caught_up" {
			caught++
		}
		times = append(times, when(m.ScheduledAt, c.Now()))
	}
	if len(times) == 0 {
		return
	}
	key, state := "missed-"+row.Server+"/"+row.Instance, strings.Join(times, ",")
	if a.prefs.Dismissed[key] == state {
		return
	}
	ui.Column(c).Gap(6).Padding(12, 14).Radius(12).Background(palette.TroubleBg).Border(1, palette.TroubleBorder).Children(func() {
		ui.Text(c, T("missed.title", "n", len(times))).Bold().TextColor(palette.Warn)
		line := strings.Join(times, T("common.list_sep"))
		if caught > 0 {
			line += T("common.dot_sep") + T("missed.caught_up", "n", caught)
		}
		muted(ui.Text(c, line)).Wrap()
		ui.Row(c).Gap(8).Children(func() {
			ui.Button(c, T("missed.read")).OnClick(func() {
				a.prefs.Dismissed[key] = state
				a.savePrefs()
			})
		})
	})
}
