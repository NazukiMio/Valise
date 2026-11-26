package gui

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/identity"
	"valise/core/protection"
	"valise/core/vault"
	"valise/core/vaultrpc"
	"valise/ui/service"
)

// The agent's “管理与设置” tab (docs/UI.md 9.5, 7.8): its protection plan,
// each change prepared, confirmed here and checked by the server.

// protState is what was read and is being done for one agent's plan.
type protState struct {
	ref         *identity.AgentRef
	view        *protection.View
	localCopies *service.LocalCopiesView
	err         error
	read        job

	sched   *schedEdit
	tier    *tierEdit
	copyTo  *copyEdit
	changeJ job
	task    *service.ProtectionTask

	backup              *backupRun
	otherBackups        []*backupRun
	backupReadErr       error
	newBackupConfirm    bool
	oneOff              *oneOffBackup
	oneOffSupported     bool
	savepointsSupported bool
}

func (a *App) protOf(row *agentRow) *protState {
	key := row.Server + "/" + row.Instance
	var ref *identity.AgentRef
	for _, it := range a.agents.value.Items {
		if it.ConnectionID == row.Server && it.Instance.InstanceID == row.Instance && it.AgentRef != nil {
			r := *it.AgentRef
			ref = &r
		}
	}
	// The agent list is read again after a re-enrollment; a new identity
	// starts over rather than keep reading with the old one.
	if ps := a.prots[key]; ps == nil || !sameRef(ps.ref, ref) {
		ps = &protState{ref: ref}
		a.prots[key] = ps
		a.readProt(row.Server, ps)
	}
	return a.prots[key]
}

func sameRef(x, y *identity.AgentRef) bool {
	return x == nil && y == nil || x != nil && y != nil && *x == *y
}

func (a *App) readProt(host string, ps *protState) {
	if ps.ref == nil {
		return
	}
	ref := *ps.ref
	var v protection.View
	var lc *service.LocalCopiesView
	var lcErr error
	var tasks []service.BackupTask
	var tasksErr error
	var once, savepoints bool
	a.start(&ps.read, time.Minute, func(ctx context.Context) (err error) {
		tasks, tasksErr = a.svc.ListBackupTasks(ctx)
		v, err = a.svc.GetProtection(ctx, host, protection.GetParams{AgentRef: ref})
		if err == nil {
			caps, e := a.svc.BackupCapabilities(ctx, host)
			once = e == nil && slices.Contains(caps, protection.BackupCapabilityV3)
			savepoints = e == nil && slices.Contains(caps, vaultrpc.SavepointsCapability)
			lc, lcErr = a.svc.LocalCopies(ctx, host, ref.InstanceID)
		}
		return err
	}, func(err error) {
		ps.err = err
		ps.oneOffSupported, ps.savepointsSupported = once, savepoints
		ps.backupReadErr = tasksErr
		if tasksErr == nil {
			a.restoreBackupCards(ps, host, tasks)
		}
		if err == nil {
			ps.view = &v
			if lcErr == nil && lc != nil {
				ps.localCopies = lc
			}
		}
	})
}

func (a *App) manageTab(c *ui.Context, row *agentRow) {
	ps := a.protOf(row)
	if ps.ref == nil {
		card(ui.Column(c)).Gap(10).Children(func() {
			ui.Text(c, T("protect.no_ref")).Wrap()
			a.renderInlineReenroll(c, row, T("protect.reenroll"), T("protect.go_server_agents"))
		})
		return
	}
	a.nativeCard(c, row)
	if ps.read.busy && ps.view == nil {
		a.jobLine(c, &ps.read)
		return
	}
	if ps.err != nil {
		errorBox(c, ps.err, a.fold("prot-err-"+row.Instance))
	}
	if ps.backup != nil {
		a.backupProgress(c, row, ps)
	}
	for i, br := range ps.otherBackups {
		a.backupCard(c.Key("previous-backup-"+br.task.TaskRef.TaskID), row, ps.otherBackups[i])
	}
	if ps.backupReadErr != nil {
		errorBox(c, ps.backupReadErr, a.fold("backup-read-"+row.Instance))
	}
	if ps.view == nil {
		return
	}
	a.missedCard(c, row, ps)
	if ps.view.SupersededPlan != nil {
		card(ui.Row(c)).Gap(14).AlignItems(ui.Center).Border(1, palette.TroubleBorder).Children(func() {
			ui.Text(c, T("protect.superseded_what")).Grow(1).Wrap()
			if !ps.changeJ.busy {
				ui.PrimaryButton(c, T("protect.superseded_go")).OnClick(func() { a.applyDesired(row, ps, ps.desired()) })
			}
		})
	}
	d := ps.desired()
	card(ui.Column(c)).Gap(8).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			sectionTitle(ui.Text(c, T("protect.title"))).Grow(1)
			a.planTag(c, ps)
			if ps.view.ServerPlan != nil && !ps.backupBlocked() {
				if ps.oneOffSupported {
					ui.Button(c, T("backup.once.title")).OnClick(func() { ps.oneOff = &oneOffBackup{tier: "full"} })
				}
				if ps.hasUnknownBackup() {
					ui.Button(c, T("backup.new")).OnClick(func() { ps.newBackupConfirm = true })
				} else {
					ui.PrimaryButton(c, T("backup.now")).OnClick(func() { a.startBackupNow(row, ps) })
				}
			}
		})
		settingRow(c, T("protect.what"), tierText(orText(d.Scope.Tier, vault.TierFull)), "", func() {
			ui.Button(c, T("protect.change_tier")).OnClick(func() { ps.tier = newTierEdit(d) })
		})
		settingRow(c, T("protect.schedule"), scheduleText(d.Schedule), "", func() {
			ui.Button(c, T("protect.change")).OnClick(func() { ps.sched = newSchedEdit(d) })
		})
		settingRow(c, T("protect.period"), T("protect.period."+orText(d.Period.Unit, "month")), "", nil)
		if ps.savepointsSupported {
			keep := T("protect.retention.off")
			if d.Period.KeepAutoPeriods > 0 {
				keep = T("protect.retention.count", "n", d.Period.KeepAutoPeriods)
			}
			settingRow(c, T("protect.retention"), keep, "", nil)
		}
		settingRow(c, T("protect.copies"), a.copiesText(ps), "", func() {
			ui.Button(c, T("protect.change")).OnClick(func() { a.openCopyTo(row, ps) })
		})
		if ps.changeJ.busy {
			a.jobLine(c, &ps.changeJ)
		}
		if ps.changeJ.err != nil {
			errorBox(c, ps.changeJ.err, a.fold("chg-err-"+row.Instance))
		}
	})
	if ps.sched != nil {
		a.scheduleDialog(c, row, ps)
	}
	if ps.tier != nil {
		a.tierDialog(c, row, ps)
	}
	if ps.copyTo != nil {
		a.copyToDialog(c, row, ps)
	}
	if ps.oneOff != nil {
		a.oneOffDialog(c, row, ps)
	}
	if ps.newBackupConfirm {
		open := true
		if ui.Modal(c, &open, func() {
			dialog(ui.Column(c), 440).Children(func() {
				dialogTitle(ui.Text(c, T("backup.new")))
				ui.Text(c, T("backup.new_warning")).Wrap()
				ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
					ui.Button(c, T("common.cancel")).OnClick(func() { ps.newBackupConfirm = false })
					ui.PrimaryButton(c, T("backup.new_confirm")).OnClick(func() {
						ps.newBackupConfirm = false
						a.startBackupNow(row, ps)
					})
				})
			})
		}).Dismissed() {
			ps.newBackupConfirm = false
		}
	}
}

func (ps *protState) backupBlocked() bool {
	if ps.backupReadErr != nil {
		return true
	}
	for _, br := range append([]*backupRun{ps.backup}, ps.otherBackups...) {
		if br != nil && (br.j.busy || br.task != nil && backupUnresolved(br.task.State) && br.task.State != "reconciling") {
			return true
		}
	}
	return false
}

func (ps *protState) hasUnknownBackup() bool {
	for _, br := range append([]*backupRun{ps.backup}, ps.otherBackups...) {
		if br != nil && br.task != nil && br.task.State == "reconciling" {
			return true
		}
	}
	return false
}

// planTag says where the plan stands: in force and checked, waiting to be
// checked, or none yet.
func (a *App) planTag(c *ui.Context, ps *protState) {
	switch {
	case len(ps.view.PendingChanges) > 0:
		ui.Text(c, T("protect.pending")).FontSize(12).TextColor(palette.Warn)
	case ps.view.ServerPlan != nil:
		plainTag(c, T("protect.in_force", "n", ps.view.ServerPlan.Revision))
	case ps.view.SupersededPlan != nil:
		ui.Text(c, T("protect.superseded")).FontSize(12).TextColor(palette.Warn)
	default:
		plainTag(c, T("protect.none"))
	}
}

func (a *App) copiesText(ps *protState) string {
	d := ps.desired()
	sep := T("common.list_sep")
	var groups []string
	var serverNames []string
	for _, cp := range d.ServerCopies {
		if cp.Enabled == nil || *cp.Enabled {
			serverNames = append(serverNames, cp.TargetRef.ID)
		}
	}
	if len(serverNames) > 0 {
		groups = append(groups, T("protect.copies_server", "names", strings.Join(serverNames, sep)))
	}
	if ps.localCopies != nil {
		var repoNames, cloudNames []string
		for _, c := range ps.localCopies.Repos {
			if (c.AfterBackup != nil && *c.AfterBackup) || (c.OnClientOpen != nil && *c.OnClientOpen) {
				repoNames = append(repoNames, c.RepoID)
			}
		}
		if len(repoNames) > 0 {
			groups = append(groups, T("protect.copies_local", "names", strings.Join(repoNames, sep)))
		}
		for _, c := range ps.localCopies.Clouds {
			if (c.AfterBackup != nil && *c.AfterBackup) || (c.OnClientOpen != nil && *c.OnClientOpen) {
				cloudNames = append(cloudNames, c.RepoID)
			}
		}
		if len(cloudNames) > 0 {
			groups = append(groups, T("protect.copies_cloud", "names", strings.Join(cloudNames, sep)))
		}
	}
	if len(groups) == 0 {
		return T("protect.copies_none")
	}
	return strings.Join(groups, T("common.dot_sep"))
}

// applyDesired prepares the change, confirms it (the person's click is the
// confirmation) and follows it until the server checked it. A plan set
// again after a re-enrollment goes on from the old plan's revision.
func (a *App) applyDesired(row *agentRow, ps *protState, d protection.Desired) {
	var rev protection.Revision
	switch {
	case ps.view != nil && ps.view.ServerPlan != nil:
		rev = ps.view.ServerPlan.Revision
	case ps.view != nil && ps.view.SupersededPlan != nil:
		rev = ps.view.SupersededPlan.Revision
	}
	d = ensureScheduleDefaults(d)
	host, ref := row.Server, *ps.ref
	a.start(&ps.changeJ, 5*time.Minute, func(ctx context.Context) error {
		a.says(&ps.changeJ)(T("protect.preparing"))
		fc, err := a.svc.PrepareProtectionChange(ctx, host, protection.PrepareParams{AgentRef: ref, ExpectedRevision: rev, Desired: d})
		if err != nil {
			return err
		}
		a.confirm(fc.PlanID)
		a.says(&ps.changeJ)(T("protect.applying"))
		t, err := a.svc.StartProtectionChange(ctx, fc.PlanID, "desktop-"+fc.PlanID)
		for err == nil && t.State != "succeeded" && t.State != "failed" && t.State != "cancelled" && ctx.Err() == nil {
			time.Sleep(time.Second)
			t, err = a.svc.GetProtectionTask(ctx, t.TaskRef)
		}
		if err == nil && t.Error != nil {
			err = taskErr(t.Error)
		}
		return err
	}, func(error) { a.readProt(host, ps) })
}

// localZone is this computer's IANA time zone, "UTC" when unknown.
func localZone() string {
	if tz := os.Getenv("TZ"); tz != "" {
		return tz
	}
	if p, err := os.Readlink("/etc/localtime"); err == nil {
		if _, rest, ok := strings.Cut(filepath.ToSlash(p), "zoneinfo/"); ok {
			return rest
		}
	}
	return "UTC"
}

// Legacy catalog key "protect.full" is retained for locale compatibility.
