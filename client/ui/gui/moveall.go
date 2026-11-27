package gui

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/revive"
	"valise/ui/service"
)

// moveAllState holds the harness-by-harness one-click server migration page
// state at /servers/{name}/move-all (WP6).
type moveAllState struct {
	from         string
	target       string
	inspect      *service.HarnessMoveInspection
	selected     map[string]bool // ProfileID -> selected
	lastProfiles []string
	plan         *service.HarnessMovePlan
	result       *service.HarnessMoveResult
	opts         batchReviveOpts
	confirm      bool
	j            job
}

func (a *App) moveAllFor(from string) *moveAllState {
	if a.moveAll == nil || a.moveAll.from != from {
		if a.moveAll != nil && a.moveAll.plan != nil {
			a.moveAll.plan.Close()
		}
		a.moveAll = &moveAllState{from: from, selected: map[string]bool{}}
	}
	return a.moveAll
}

func (a *App) harnessTitle(profileID string) string {
	if name := a.harness[profileID]; name != "" {
		return name
	}
	return profileID
}

func (a *App) moveAllPage(c *ui.Context, from string) {
	st := a.moveAllFor(from)
	backLink(c, from, "/servers/"+from)
	pageHead(c, T("moveall.title", "server", from), T("moveall.sub"))

	hosts := restoreTargets(a.hosts(), from, revive.ModeMigrate)
	if st.target != "" && !slices.Contains(hosts, st.target) {
		st.target = ""
	}
	if st.target == "" && len(hosts) == 1 {
		st.target = hosts[0]
	}

	if st.j.err != nil {
		errorBox(c, st.j.err, a.fold("moveall-err-"+from))
	}

	if st.result != nil {
		a.moveAllResultView(c, st)
		return
	}
	if st.plan != nil {
		a.moveAllPlanView(c, st)
		return
	}

	card(ui.Column(c)).Gap(spItem).Children(func() {
		ui.Text(c, T("moveall.target_title")).Bold()
		if len(hosts) == 0 {
			notice(c, "warn", T("restore.migrate_no_other_server"))
			return
		}
		ui.Row(c).Gap(spItem).AlignItems(ui.Center).Wrap().Children(func() {
			labeled(c, T("restore.target"), func() {
				if ui.Select(c, &st.target, hosts).Label(T("restore.target")).Changed() {
					st.inspect = nil
					st.selected = map[string]bool{}
				}
			}).Grow(1)
			ui.Button(c, T("moveall.inspect")).Disabled(st.target == "" || st.j.busy).OnClick(func() {
				a.runInspectHarnessMove(st)
			})
		})
		if st.j.busy {
			a.jobLine(c, &st.j)
		}
	})

	if st.inspect != nil {
		a.moveAllInspectView(c, st)
	}
}

func (a *App) moveAllInspectView(c *ui.Context, st *moveAllState) {
	if len(st.inspect.Groups) == 0 {
		muted(ui.Text(c, T("moveall.empty"))).FontSize(fsBody)
		return
	}
	card(ui.Column(c)).Gap(spItem).Children(func() {
		sectionTitle(ui.Text(c, T("moveall.groups_title", "n", len(st.inspect.Groups))))
		muted(ui.Text(c, T("moveall.groups_hint"))).FontSize(fsSub).Wrap()
		for _, g := range st.inspect.Groups {
			grp := g
			a.moveAllGroupCard(c.Key("ma-grp-"+grp.ProfileID), st, grp)
		}
		chosen := st.selectedProfiles()
		ui.Row(c).Justify(ui.End).Children(func() {
			ui.PrimaryButton(c, T("moveall.prepare", "n", len(chosen))).Disabled(len(chosen) == 0 || st.j.busy).OnClick(func() {
				a.runPrepareHarnessMove(st, chosen)
			})
		})
	})
}

func (a *App) moveAllGroupCard(c *ui.Context, st *moveAllState, g service.HarnessMoveGroup) {
	subPanel(ui.Column(c)).Gap(spInside).Children(func() {
		ui.Row(c).Gap(spInside).AlignItems(ui.Center).Wrap().Children(func() {
			title := a.harnessTitle(g.ProfileID)
			if g.Ready {
				checked := st.selected[g.ProfileID]
				if ui.Checkbox(c.Key("ma-chk-"+g.ProfileID), &checked, title).Changed() {
					st.selected[g.ProfileID] = checked
				}
				plainTag(c, T("moveall.group.ready"))
			} else {
				ui.Text(c, title).Bold()
				ui.Text(c, T("moveall.group.not_ready")).FontSize(fsCaption).Bold().TextColor(palette.Warn)
			}
			if g.BundleID != "" {
				plainTag(c, T("moveall.group.bundle"))
			}
		})
		if !g.Ready && g.Reason != "" {
			notice(c, "warn", g.Reason)
		}
		for _, m := range g.Members {
			label := orText(m.DisplayName, m.InstanceID)
			if m.Ready {
				tier := ""
				if m.Snapshot != nil {
					tier = tierText(m.Snapshot.Tier)
				}
				statusRow(c, "ok", label, T("moveall.member.ready", "tier", tier))
			} else {
				statusRow(c, "warn", label, orText(m.Reason, T("moveall.member.missing_full")))
			}
		}
	})
}

func (a *App) moveAllPlanView(c *ui.Context, st *moveAllState) {
	sum := summarizeRevivePlans(movePlanRevivePlans(st.plan))
	card(ui.Column(c)).Gap(spItem).Children(func() {
		sectionTitle(ui.Text(c, T("moveall.plan_title")))
		fact(c, T("restore.origin"), st.plan.From)
		fact(c, T("restore.target"), st.plan.Target)
		runnable := 0
		for _, gp := range st.plan.Groups {
			title := a.harnessTitle(gp.ProfileID)
			gPlans := groupRevivePlans(gp)
			gBlockers := 0
			for _, rp := range gPlans {
				gBlockers += planBlockerCount(rp)
			}
			switch {
			case gp.PlanError != "":
				statusRow(c, "warn", title, T("moveall.plan_err", "reason", gp.PlanError))
			case gBlockers > 0:
				statusRow(c, "warn", title, T("moveall.plan_blocked", "n", gBlockers))
			default:
				runnable++
				statusRow(c, "ok", title, T("moveall.plan_ready", "n", len(gp.Group.Members)))
			}
			for _, rp := range gPlans {
				renderRevivePlanDetails(c, "ma-rp-"+gp.ProfileID+"-"+rp.Plan.InstanceID, rp)
			}
		}
		canRun := renderBatchReviveChecks(c, "ma", sum, &st.opts, func() {
			a.runPrepareHarnessMove(st, st.lastProfiles)
		})
		ui.Checkbox(c, &st.confirm, T("moveall.confirm_check"))
		if st.j.busy {
			a.jobLine(c, &st.j)
			return
		}
		ui.Row(c).Gap(spInside).Justify(ui.End).Children(func() {
			ui.Button(c, T("common.back")).OnClick(func() {
				st.plan.Close()
				st.plan = nil
				st.confirm = false
				st.opts.resetConfirmations()
			})
			ui.PrimaryButton(c, T("moveall.run")).Disabled(!st.confirm || runnable == 0 || !canRun).OnClick(func() {
				a.runExecuteHarnessMove(st)
			})
		})
	})
}

func (a *App) moveAllResultView(c *ui.Context, st *moveAllState) {
	res := st.result
	if res.Partial {
		notice(c, "warn", T("moveall.result_partial"))
	} else {
		notice(c, "ok", T("moveall.result_ok"))
	}
	card(ui.Column(c)).Gap(spInside).Children(func() {
		for _, gr := range res.Groups {
			title := a.harnessTitle(gr.ProfileID)
			if gr.Succeeded && !gr.Partial {
				statusRow(c, "ok", title, T("moveall.group.done", "n", len(gr.Members)))
			} else {
				statusRow(c, "warn", title, T("moveall.group.failed", "reason", orText(gr.Error, T("moveall.group.failed_generic"))))
			}
			for _, m := range gr.Members {
				if m.Error != "" {
					muted(ui.Text(c, T("moveall.member.failed", "id", m.InstanceID, "reason", m.Error))).FontSize(fsCaption).Wrap()
				}
			}
		}
		if res.Partial {
			muted(ui.Text(c, T("moveall.partial_hint"))).FontSize(fsSub).Wrap()
		}
	})
	ui.Row(c).Justify(ui.End).Children(func() {
		ui.PrimaryButton(c, T("common.close")).OnClick(func() {
			a.moveAll = nil
			a.navigate("/servers/" + st.from)
		})
	})
}

func (st *moveAllState) selectedProfiles() []string {
	if st.inspect == nil {
		return nil
	}
	var out []string
	for _, g := range st.inspect.Groups {
		if g.Ready && st.selected[g.ProfileID] {
			out = append(out, g.ProfileID)
		}
	}
	return out
}

func (a *App) runInspectHarnessMove(st *moveAllState) {
	fn := a.inspectHarnessMoveFn
	if fn == nil {
		fn = a.svc.InspectHarnessMove
	}
	var insp *service.HarnessMoveInspection
	a.start(&st.j, 2*time.Minute, func(ctx context.Context) error {
		var err error
		insp, err = fn(ctx, st.from, st.target)
		return err
	}, func(err error) {
		if err == nil && insp != nil {
			st.inspect = insp
			st.selected = map[string]bool{}
			for _, g := range insp.Groups {
				if g.Ready {
					st.selected[g.ProfileID] = true
				}
			}
		}
	})
}

func (a *App) runPrepareHarnessMove(st *moveAllState, profiles []string) {
	st.lastProfiles = append([]string(nil), profiles...)
	fn := a.prepareHarnessMoveFn
	if fn == nil {
		fn = a.svc.PrepareHarnessMove
	}
	opts := service.HarnessMoveOptions{
		From:                 st.from,
		Target:               st.target,
		ProfileIDs:           profiles,
		AllowDraft:           st.opts.allowDraft,
		AllowSnapshotProfile: st.opts.allowSnapshotProfile,
		AllowVersionMismatch: st.opts.allowVersionMismatch,
		SkipService:          st.opts.skipService,
	}
	var plan *service.HarnessMovePlan
	a.start(&st.j, 15*time.Minute, func(ctx context.Context) error {
		var err error
		plan, err = fn(ctx, opts, "")
		return err
	}, func(err error) {
		if err == nil {
			if st.plan != nil {
				st.plan.Close()
			}
			st.plan = plan
			st.confirm = false
			st.opts.resetConfirmations()
		}
	})
}

func (a *App) runExecuteHarnessMove(st *moveAllState) {
	plan := st.plan
	if plan == nil {
		return
	}
	fn := a.runHarnessMoveFn
	if fn == nil {
		fn = a.svc.RunHarnessMove
	}
	confirm := st.opts.buildConfirm(st.confirm)
	var res *service.HarnessMoveResult
	a.start(&st.j, 30*time.Minute, func(ctx context.Context) error {
		var err error
		res, err = fn(ctx, plan, confirm)
		return err
	}, func(err error) {
		if err == nil {
			st.result = res
			plan.Close()
			st.plan = nil
			a.refreshAgents()
			a.refreshTimeline()
		}
	})
}

var _ = strings.TrimSpace
