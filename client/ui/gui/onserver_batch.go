package gui

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/valise"
	"valise/ui/service"
)

// WP1: 1-click re-enrollment (on server page and in-place on agent detail)
// and multi-select batch enrollment in a single owner session.

func (a *App) isActionableDetected(host string, d service.DetectedInstance) bool {
	if d.Enrolled == "" {
		return true
	}
	if a.unbound(host, d.Enrolled) {
		return true
	}
	return d.ProfileVersion > 0 && d.EnrolledProfileVersion > 0 && d.ProfileVersion > d.EnrolledProfileVersion
}

func (a *App) actionableDetected(host string, sc *serverScan) []service.DetectedInstance {
	var out []service.DetectedInstance
	for _, d := range sc.found {
		if a.isActionableDetected(host, d) {
			out = append(out, d)
		}
	}
	return out
}

func pruneSelected(sc *serverScan, found []service.DetectedInstance) {
	if len(sc.selected) == 0 {
		return
	}
	valid := make(map[string]bool, len(found))
	for _, d := range found {
		valid[d.ID] = true
	}
	for id := range sc.selected {
		if !valid[id] {
			delete(sc.selected, id)
		}
	}
}

func (a *App) clearInstanceCaches(host, instance string) {
	key := host + "/" + instance
	delete(a.natives, key)
	delete(a.agentServices, key)
	delete(a.prots, key)
}

func (a *App) doQuickReenroll(ctx context.Context, host, instance string) (*service.EnrollBatchResult, error) {
	if a.quickReenrollFn != nil {
		return a.quickReenrollFn(ctx, host, instance)
	}
	return a.svc.QuickReenroll(ctx, host, instance)
}

func (a *App) doEnrollBatch(ctx context.Context, host string, items []service.EnrollBatchItem) (*service.EnrollBatchResult, error) {
	if a.enrollBatchFn != nil {
		return a.enrollBatchFn(ctx, host, items)
	}
	return a.svc.EnrollBatch(ctx, host, items)
}

func (a *App) quickReenrollOnServer(host string, sc *serverScan, d service.DetectedInstance) {
	ad := sc.adopts[d.ID]
	if ad == nil {
		ad = &adoption{assign: map[string]string{}}
		sc.adopts[d.ID] = ad
	}
	if ad.busy {
		return
	}
	inst := orText(d.Enrolled, d.ID)
	ad.busy, ad.err = true, nil
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		res, err := a.doQuickReenroll(ctx, host, inst)
		a.update(func() {
			ad.busy = false
			if err != nil {
				ad.err = err
				return
			}
			if res != nil && len(res.Failures) > 0 && len(res.Enrolled) == 0 {
				if res.Failures[0].Err != nil {
					ad.err = res.Failures[0].Err
				} else {
					ad.err = valise.New("reenroll_failed", res.Failures[0].Reason, "")
				}
				return
			}
			delete(sc.adopts, d.ID)
			delete(sc.selected, d.ID)
			a.clearInstanceCaches(host, inst)
			a.refreshAgents()
			a.scanServer(host)
		})
	})
}

func (a *App) reenrollJob(host, instance string) *job {
	if a.reenrollJobs == nil {
		a.reenrollJobs = map[string]*job{}
	}
	key := host + "/" + instance
	j := a.reenrollJobs[key]
	if j == nil {
		j = &job{}
		a.reenrollJobs[key] = j
	}
	return j
}

func (a *App) runQuickReenrollInPlace(host, instance string) {
	j := a.reenrollJob(host, instance)
	if j.busy {
		return
	}
	var res *service.EnrollBatchResult
	a.start(j, 3*time.Minute, func(ctx context.Context) error {
		var err error
		res, err = a.doQuickReenroll(ctx, host, instance)
		if err != nil {
			return err
		}
		if res != nil && len(res.Failures) > 0 && len(res.Enrolled) == 0 {
			if res.Failures[0].Err != nil {
				return res.Failures[0].Err
			}
			return valise.New("reenroll_failed", res.Failures[0].Reason, "")
		}
		return nil
	}, func(err error) {
		if err != nil {
			return
		}
		a.clearInstanceCaches(host, instance)
		a.refreshAgents()
		if a.scans[host] != nil {
			a.scanServer(host)
		}
	})
}

func actionButton(c *ui.Context, label string, primary bool, onClick func()) {
	if primary {
		ui.PrimaryButton(c, label).OnClick(onClick)
		return
	}
	ui.Button(c, label).OnClick(onClick)
}

func (a *App) renderInlineReenroll(c *ui.Context, row *agentRow, primaryLabel, secondaryLabel string) {
	j := a.reenrollJob(row.Server, row.Instance)
	if j.busy {
		a.jobLine(c, j)
		return
	}
	if j.err != nil {
		errorBox(c, j.err, a.fold("reenroll-err-"+row.Server+"/"+row.Instance))
	}
	ui.Row(c).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
		ui.Button(c, primaryLabel).OnClick(func() {
			a.runQuickReenrollInPlace(row.Server, row.Instance)
		})
		quietButton(c, secondaryLabel).OnClick(func() {
			a.router.Push("/servers/" + row.Server + "/agents")
		})
	})
}

func (a *App) onServerBatchBar(c *ui.Context, host string, sc *serverScan, actionable []service.DetectedInstance) {
	if sc.selected == nil {
		sc.selected = map[string]bool{}
	}
	selectedCount := 0
	for _, d := range actionable {
		if sc.selected[d.ID] {
			selectedCount++
		}
	}
	allSelected := len(actionable) > 0 && selectedCount == len(actionable)

	card(ui.Column(c)).Gap(8).Children(func() {
		if len(actionable) >= 2 {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Wrap().Children(func() {
				if ui.Checkbox(c, &allSelected, T("onserver.batch.select_all")).Changed() {
					if allSelected {
						for _, d := range actionable {
							sc.selected[d.ID] = true
						}
					} else {
						for _, d := range actionable {
							delete(sc.selected, d.ID)
						}
					}
				}
				muted(ui.Text(c, T("onserver.batch.selected", "n", selectedCount, "total", len(actionable)))).FontSize(fsSub).Grow(1)
				needTag(c, T("need.valise"))
				ui.PrimaryButton(c, T("onserver.batch.run", "n", selectedCount)).
					Disabled(selectedCount == 0 || sc.batchJ.busy).
					OnClick(func() { a.runOnServerBatch(host, sc, actionable) })
			})
			ui.Checkbox(c, &sc.batchEnablePlan, T("onserver.batch.enable_plan"))
		}
		if sc.batchJ.busy {
			a.jobLine(c, &sc.batchJ)
		}
		if sc.batchJ.err != nil {
			errorBox(c, sc.batchJ.err, a.fold("batch-enroll-err-"+host))
		}
		if sc.batchRes != nil {
			a.onServerBatchSummary(c, sc.batchRes)
		}
	})
}

func (a *App) runOnServerBatch(host string, sc *serverScan, actionable []service.DetectedInstance) {
	if sc.batchJ.busy {
		return
	}
	var items []service.EnrollBatchItem
	for _, d := range actionable {
		if !sc.selected[d.ID] {
			continue
		}
		item := service.EnrollBatchItem{
			ProfileID:  d.ProfileID,
			Name:       d.Name,
			InstanceID: orText(d.Enrolled, d.ID),
		}
		if d.Enrolled == "" {
			item.EnableDefaultPlan = sc.batchEnablePlan
		} else {
			item.ReactivatePlan = true
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		return
	}
	sc.batchRes = nil
	var res *service.EnrollBatchResult
	a.start(&sc.batchJ, 5*time.Minute, func(ctx context.Context) error {
		var err error
		res, err = a.doEnrollBatch(ctx, host, items)
		return err
	}, func(err error) {
		if res != nil {
			sc.batchRes = res
			for _, id := range res.Enrolled {
				a.clearInstanceCaches(host, id)
			}
		}
		if err == nil {
			sc.selected = map[string]bool{}
		}
		if res != nil && len(res.Enrolled) > 0 {
			a.refreshAgents()
			a.scanServer(host)
		}
	})
}

func (a *App) onServerBatchSummary(c *ui.Context, res *service.EnrollBatchResult) {
	tone := "ok"
	if len(res.Failures) > 0 {
		tone = "warn"
	}
	subPanel(ui.Column(c)).Gap(6).Children(func() {
		statusRow(c, tone, T("onserver.batch.summary_label"),
			T("onserver.batch.summary", "enrolled", len(res.Enrolled), "plans", len(res.PlanApplied), "failed", len(res.Failures)))
		if len(res.PlanNotes) > 0 {
			ids := make([]string, 0, len(res.PlanNotes))
			for id := range res.PlanNotes {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			for _, id := range ids {
				muted(ui.Text(c.Key("bpn-"+id), id+": "+res.PlanNotes[id])).FontSize(fsSub).Wrap()
			}
		}
		for i, f := range res.Failures {
			label := strings.TrimSpace(orText(f.InstanceID, f.Label))
			ui.Text(c.Key("bpf-"+itoa(i)), T("onserver.batch.failure_item", "name", label, "reason", f.Reason)).
				FontSize(fsSub).TextColor(palette.Warn).Wrap()
		}
	})
}
