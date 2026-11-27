package gui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/revive"
	"valise/core/valise"
	"valise/ui/service"
)

// bundleRestoreFlow manages same-harness bundle restore/migration when a
// snapshot belongs to a multi-member bundle and cannot be restored standalone (WP6).
type bundleRestoreFlow struct {
	from, target, mode, bundleID, source string
	members                              []service.BundleRestoreMember
	plan                                 *service.BundleRestorePlan
	result                               *service.BundleRestoreResult
	opts                                 batchReviveOpts
	confirmed                            bool
	j                                    job
}

func extractBundleNotStandalone(err error) *service.BundleNotStandaloneError {
	if err == nil {
		return nil
	}
	var berr *service.BundleNotStandaloneError
	if errors.As(err, &berr) {
		return berr
	}
	if ue, ok := valise.As(err); ok && ue.Code == "bundle_not_standalone" {
		return &service.BundleNotStandaloneError{UserErr: ue}
	}
	return nil
}

func (a *App) openBundleRestoreFromFlow(r *restoreFlow, bundleID string, memberIDs []string) {
	if bundleID == "" {
		bundleID = r.point.BundleID
	}
	if len(memberIDs) == 0 {
		memberIDs = r.point.BundleMembers
	}
	nameByInst := map[string]string{}
	for _, it := range a.agents.value.Items {
		if it.ConnectionID == r.origin {
			nameByInst[it.Instance.InstanceID] = it.Instance.Name
		}
	}
	snapByInst := map[string]string{}
	if bundleID != "" {
		for _, pt := range a.timeline.value.Items {
			if pt.BundleID == bundleID && pt.RecordedInstanceID != "" {
				snapByInst[pt.RecordedInstanceID] = pt.SnapshotRef.SnapshotID
			}
		}
	}
	var members []service.BundleRestoreMember
	for _, id := range memberIDs {
		name := nameByInst[id]
		snap := snapByInst[id]
		if snap == "" && id == r.point.RecordedInstanceID {
			snap = r.point.SnapshotRef.SnapshotID
		}
		if snap != "" {
			members = append(members, service.BundleRestoreMember{
				InstanceID:  id,
				SnapshotID:  snap,
				Name:        name,
				SkipService: name != "",
			})
		}
	}
	if bundleID != "" && len(memberIDs) > 0 && len(members) < len(memberIDs) {
		// Incomplete local timeline view (e.g. from /locations/<name>): leave
		// Members empty so PrepareBundleRestore resolves all members authoritatively.
		members = nil
	}
	srcSpec := ""
	if r.source >= 0 && r.source < len(r.sources) {
		srcSpec = r.sources[r.source].Spec
	}
	mode := r.mode
	if mode == "" || mode == revive.ModeClone {
		mode = revive.ModeRollback
	}
	target := r.target
	if mode == revive.ModeRollback {
		target = r.origin
	}
	a.bundleRestore = &bundleRestoreFlow{
		from:     r.origin,
		target:   target,
		mode:     mode,
		bundleID: bundleID,
		source:   srcSpec,
		members:  members,
	}
}

func (a *App) bundleInterceptBanner(c *ui.Context, r *restoreFlow) {
	berr := r.bundleErr
	if berr == nil {
		return
	}
	shown := strings.Join(berr.Others, T("common.list_sep"))
	if shown == "" {
		shown = strings.Join(berr.Members, T("common.list_sep"))
	}
	if shown == "" {
		shown = otherBundleMembers(r.point)
	}
	card(ui.Column(c)).Gap(spInside).Border(1, palette.TroubleBorder).Background(palette.TroubleBg).Children(func() {
		notice(c, "warn", T("bundle.intercept.msg", "members", shown))
		btnLabel := T("bundle.switch_restore")
		if r.mode == revive.ModeMigrate {
			btnLabel = T("bundle.switch_migrate")
		}
		ui.Row(c).Justify(ui.End).Children(func() {
			ui.Button(c, btnLabel).OnClick(func() {
				a.openBundleRestoreFromFlow(r, berr.BundleID, berr.Members)
			})
		})
	})
}

func (a *App) bundleRestorePage(c *ui.Context) {
	br := a.bundleRestore
	if br == nil {
		return
	}
	ui.Row(c).Gap(spInside).AlignItems(ui.Center).Children(func() {
		quietButton(c, T("common.back")).OnClick(func() {
			if br.plan != nil {
				br.plan.Close()
			}
			a.bundleRestore = nil
		})
	})
	pageHead(c, T("bundle.restore.title"), T("bundle.restore.sub"))
	if br.j.err != nil {
		errorBox(c, br.j.err, a.fold("bundle-restore-err"))
	}
	if br.result != nil {
		a.bundleRestoreResultView(c, br)
		return
	}
	if br.plan != nil {
		a.bundleRestorePlanView(c, br)
		return
	}
	card(ui.Column(c)).Gap(spItem).Children(func() {
		ui.Text(c, T("restore.how")).Bold()
		ui.RadioGroup(c, func() {
			for _, m := range []string{revive.ModeRollback, revive.ModeMigrate} {
				if ui.Radio(c.Key("bm-"+m), &br.mode, m, T("restore.mode."+m)).Changed() {
					if m == revive.ModeRollback {
						br.target = br.from
					} else if br.target == br.from {
						br.target = ""
					}
				}
			}
		}).Row().Gap(spGroup).Label(T("restore.how"))
		if br.mode == revive.ModeMigrate {
			hosts := restoreTargets(a.hosts(), br.from, revive.ModeMigrate)
			if br.target == "" && len(hosts) == 1 {
				br.target = hosts[0]
			}
			if len(hosts) == 0 {
				notice(c, "warn", T("restore.migrate_no_other_server"))
			} else {
				labeled(c, T("restore.target"), func() {
					ui.Select(c, &br.target, hosts).Label(T("restore.target"))
				})
			}
		} else {
			fact(c, T("restore.target"), br.from)
		}
		if br.j.busy {
			a.jobLine(c, &br.j)
			return
		}
		canPrep := br.mode == revive.ModeRollback || (br.target != "" && br.target != br.from)
		ui.Row(c).Justify(ui.End).Children(func() {
			ui.PrimaryButton(c, T("bundle.restore.prepare")).Disabled(!canPrep).OnClick(func() { a.prepareBundleRestore(br) })
		})
	})
}

func (a *App) bundleRestorePlanView(c *ui.Context, br *bundleRestoreFlow) {
	bp := br.plan
	sum := summarizeRevivePlans(bp.Plans)
	card(ui.Column(c)).Gap(spItem).Children(func() {
		ui.Text(c, T("bundle.restore.plan_title")).Bold()
		fact(c, T("restore.origin"), bp.From)
		fact(c, T("restore.target"), bp.Target)
		fact(c, T("bundle.restore.order"), strings.Join(bp.Order, " → "))
		for i, rp := range bp.Plans {
			role := T("bundle.member.main")
			if i > 0 || rp.DataOnly {
				role = T("bundle.member.sub_skip_service")
			}
			if n := planBlockerCount(rp); n > 0 {
				statusRow(c, "warn", rp.Plan.InstanceID, T("bundle.member.blocked", "n", n))
			} else {
				statusRow(c, "ok", rp.Plan.InstanceID, role)
			}
			renderRevivePlanDetails(c, "br-rp-"+rp.Plan.InstanceID, rp)
		}
		canRun := renderBatchReviveChecks(c, "br", sum, &br.opts, func() { a.prepareBundleRestore(br) })
		ui.Checkbox(c, &br.confirmed, T("bundle.restore.confirm_check"))
		if br.j.busy {
			a.jobLine(c, &br.j)
			return
		}
		ui.Row(c).Gap(spInside).Justify(ui.End).Children(func() {
			ui.Button(c, T("common.back")).OnClick(func() {
				bp.Close()
				br.plan = nil
				br.confirmed = false
				br.opts.resetConfirmations()
			})
			ui.PrimaryButton(c, T("bundle.restore.run")).Disabled(!br.confirmed || !canRun).OnClick(func() { a.runBundleRestore(br) })
		})
	})
}

func (a *App) bundleRestoreResultView(c *ui.Context, br *bundleRestoreFlow) {
	tone := "ok"
	msg := T("bundle.restore.result_ok")
	if br.result.Partial {
		tone = "warn"
		msg = T("bundle.restore.result_partial")
	}
	notice(c, tone, msg)
	card(ui.Column(c)).Gap(spInside).Children(func() {
		for _, mr := range br.result.Members {
			if mr.Error != "" {
				statusRow(c, "warn", mr.InstanceID, mr.Error)
			} else {
				statusRow(c, "ok", mr.InstanceID, T("bundle.restore.member_ok"))
			}
		}
	})
	ui.Row(c).Justify(ui.End).Children(func() {
		ui.PrimaryButton(c, T("common.close")).OnClick(func() {
			a.bundleRestore = nil
			a.navigate("/agents")
		})
	})
}

func (a *App) prepareBundleRestore(br *bundleRestoreFlow) {
	opts := service.BundleRestoreOptions{
		Target:               br.target,
		From:                 br.from,
		Mode:                 br.mode,
		BundleID:             br.bundleID,
		Members:              br.members,
		Source:               br.source,
		AllowDraft:           br.opts.allowDraft,
		AllowSnapshotProfile: br.opts.allowSnapshotProfile,
		AllowVersionMismatch: br.opts.allowVersionMismatch,
		SkipService:          br.opts.skipService,
	}
	fn := a.prepareBundleRestoreFn
	if fn == nil {
		fn = a.svc.PrepareBundleRestore
	}
	var bp *service.BundleRestorePlan
	a.start(&br.j, 15*time.Minute, func(ctx context.Context) error {
		var err error
		bp, err = fn(ctx, opts, "")
		return err
	}, func(err error) {
		if err == nil {
			if br.plan != nil {
				br.plan.Close()
			}
			br.plan = bp
			br.confirmed = false
			br.opts.resetConfirmations()
		}
	})
}

func (a *App) runBundleRestore(br *bundleRestoreFlow) {
	bp := br.plan
	if bp == nil {
		return
	}
	fn := a.runBundleRestoreFn
	if fn == nil {
		fn = a.svc.RunBundleRestore
	}
	confirm := br.opts.buildConfirm(br.confirmed)
	var res *service.BundleRestoreResult
	a.start(&br.j, 30*time.Minute, func(ctx context.Context) error {
		var err error
		res, err = fn(ctx, bp, confirm)
		return err
	}, func(err error) {
		if err == nil {
			br.result = res
			bp.Close()
			br.plan = nil
			a.refreshAgents()
			a.refreshTimeline()
		}
	})
}

// Unused import guard for slices in case future filters use it.
var _ = slices.Contains[[]string, string]
