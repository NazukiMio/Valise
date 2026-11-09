package gui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/protection"
	"valise/core/valise"
	"valise/ui/service"
)

// “现在备份” (docs/UI.md 9.5): a backup of the plan in force, each part
// said apart: written to the server's backup library, copied elsewhere.

type backupRun struct {
	j    job
	task *service.BackupTask
}

// confirmations are the plans the person confirmed in this window; the
// service's review hooks ask here before anything runs.
var confirmations struct {
	sync.Mutex
	ids map[string]bool
}

func (a *App) confirm(planID string) {
	confirmations.Lock()
	defer confirmations.Unlock()
	if confirmations.ids == nil {
		confirmations.ids = map[string]bool{}
	}
	confirmations.ids[planID] = true
}

// confirmed takes a plan's confirmation once.
func confirmed(planID string) bool {
	confirmations.Lock()
	defer confirmations.Unlock()
	ok := confirmations.ids[planID]
	delete(confirmations.ids, planID)
	return ok
}

// installReviews makes the window the person who confirms plan changes and
// backups: only a plan confirmed here goes on.
func (a *App) installReviews() {
	a.svc.BackupReview = func(_ context.Context, _ string, p service.BackupPlan) (bool, error) { return confirmed(p.PlanID), nil }
	a.svc.ProtectionReview = func(_ context.Context, _ string, p protection.FrozenChange) (bool, error) {
		return confirmed(p.PlanID), nil
	}
}

func taskErr(e *service.ProtectionTaskError) error {
	return valise.New(e.Code, e.Reason, e.Suggestion)
}

// startBackupNow makes sure the key exists and was backed up (the first
// time), then registers it on the server if needed and backs up.
func (a *App) startBackupNow(row *agentRow, ps *protState) {
	a.needKey(func() { a.runBackup(row, ps) })
}

func (a *App) runBackup(row *agentRow, ps *protState) {
	br := &backupRun{}
	ps.backup = br
	host, ref := row.Server, *ps.ref
	a.start(&br.j, 6*time.Hour, func(ctx context.Context) error {
		say := a.says(&br.j)
		say(T("backup.registering"))
		if err := a.ensureRegistered(ctx, host); err != nil {
			return err
		}
		say(T("backup.preparing"))
		plan, err := a.svc.PrepareBackup(ctx, host, ref)
		if err != nil {
			return err
		}
		a.confirm(plan.PlanID)
		t, err := a.svc.StartBackup(ctx, plan.PlanID, "desktop-"+plan.PlanID)
		if err != nil {
			return err
		}
		a.update(func() { br.task = &t })
		ch, stop, err := a.svc.WatchBackupTask(ctx, t.TaskRef)
		if err != nil {
			return err
		}
		defer stop()
		for t := range ch {
			a.update(func() { br.task = &t })
			if t.Error != nil && (t.State == "failed" || t.State == "cancelled") {
				return taskErr(t.Error)
			}
		}
		return nil
	}, func(error) { a.readProt(host, ps) })
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
	br := ps.backup
	card(ui.Column(c)).Gap(8).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, T("backup.title", "name", row.Name)).FontSize(16).Bold().Grow(1)
			if !br.j.busy {
				ui.Button(c, T("common.close")).OnClick(func() { ps.backup = nil })
			}
		})
		var run *protection.BackupRun
		if br.task != nil {
			run = br.task.Run
		}
		if run == nil {
			if br.j.busy {
				a.jobLine(c, &br.j)
			}
		} else {
			for i, comp := range run.Components {
				componentLine(c.Key(fmt.Sprint("comp-", i)), comp, run, br.j.busy)
			}
			if br.j.busy {
				ui.Row(c).Justify(ui.End).Children(func() {
					ui.Button(c, T("common.stop")).OnClick(func() { a.cancelBackup(br) })
				})
			}
		}
		if br.j.err != nil {
			errorBox(c, br.j.err, a.fold("bk-err-"+row.Instance))
		}
		if br.task != nil && br.task.Hint != "" && br.j.busy {
			muted(ui.Text(c, T("backup.detach_hint"))).Wrap()
		}
	})
}

// componentLine is one part of a backup: the server's own copy, or a copy
// to one location; each says its own state.
func componentLine(c *ui.Context, comp protection.BackupComponent, run *protection.BackupRun, busy bool) {
	name := T("backup.part.server")
	if comp.Kind == "server_copy" && comp.TargetRef != nil {
		name = T("backup.part.copy", "name", comp.TargetRef.ID)
	}
	mark, ink, state := "○", palette.TextSub, T("backup.state.waiting")
	switch comp.State {
	case "succeeded":
		mark, ink, state = "✓", palette.Ok, T("backup.state.done")
	case "failed":
		mark, ink, state = "✗", palette.Warn, T("backup.state.failed")
	case "unknown":
		mark, ink, state = "?", palette.Warn, T("backup.state.unknown")
	case "skipped":
		state = T("backup.state.skipped")
	case "running":
		mark, ink, state = "▸", palette.Text, T("backup.state.running")
	}
	ui.Row(c).Gap(12).Padding(8, 12).Radius(8).Background(palette.Board).AlignItems(ui.Center).Children(func() {
		ui.Text(c, mark).Bold().TextColor(ink).Width(16)
		ui.Text(c, name).Bold().Width(200)
		muted(ui.Text(c, state)).Grow(1)
		if mark == "▸" && busy {
			ui.Spinner(c)
		}
	})
}

func (a *App) cancelBackup(br *backupRun) {
	if br.task == nil {
		a.stopJob(&br.j)
		return
	}
	ref := br.task.TaskRef
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_, _ = a.svc.CancelBackupTask(ctx, ref)
	})
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
