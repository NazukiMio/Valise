package gui

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/ui/service"
)

func (a *App) prevDropDialog(c *ui.Context, host string, st *cleanupState) {
	e := *st.askPrev
	open := true
	if ui.Modal(c, &open, func() {
		dialog(ui.Column(c), 460).Children(func() {
			dialogTitle(ui.Text(c, T("cleanup.drop_title")))
			mono(muted(ui.Text(c, e.Path))).Wrap()
			muted(ui.Text(c, T("cleanup.prev_meta", "when", e.Created.Local().Format("2006-01-02 15:04"), "size", size(e.Bytes)))).Wrap()
			notice(c, "warn", T("cleanup.drop_warn"))
			if st.dropJ.busy {
				a.jobLine(c, &st.dropJ)
				return
			}
			if st.dropJ.err != nil {
				errorBox(c, st.dropJ.err, a.fold("cleanup-prev-dlg-"+host))
			}
			ui.Row(c).Gap(8).AlignItems(ui.Center).Justify(ui.End).Children(func() {
				needTag(c, prevDropNeed(e.Created, c.Now()))
				ui.Button(c, T("common.cancel")).OnClick(func() { st.askPrev = nil; st.dropJ.err = nil })
				dangerButton(c, T("cleanup.drop_go")).OnClick(func() {
					ops := a.cleanup()
					var removed []string
					a.start(&st.dropJ, 2*time.Minute, func(ctx context.Context) error {
						var err error
						removed, err = ops.prevDrop(ctx, host, e.OpID)
						return err
					}, func(err error) {
						if err == nil {
							st.askPrev = nil
							delete(st.selPrev, e.OpID)
							st.note = T("cleanup.dropped", "n", max(len(removed), 1))
							a.readCleanup(host)
						}
					})
				})
			})
		})
	}).Dismissed() && !st.dropJ.busy {
		st.askPrev = nil
		st.dropJ.err = nil
	}
}

func (a *App) oldCleanupDialog(c *ui.Context, host string, st *cleanupState) {
	e := *st.askOld
	open := true
	if ui.Modal(c, &open, func() {
		dialog(ui.Column(c), 480).Children(func() {
			dialogTitle(ui.Text(c, T("cleanup.old_title_ask", "id", e.InstanceID)))
			for _, l := range e.Leftovers {
				mono(muted(ui.Text(c, l.Path+" ("+size(l.Bytes)+")"))).Wrap()
			}
			notice(c, "warn", T("cleanup.old_warn"))
			if st.dropJ.busy {
				a.jobLine(c, &st.dropJ)
				return
			}
			if st.dropJ.err != nil {
				errorBox(c, st.dropJ.err, a.fold("cleanup-old-dlg-"+host))
			}
			ui.Row(c).Gap(8).AlignItems(ui.Center).Justify(ui.End).Children(func() {
				needTag(c, T("need.admin"))
				ui.Button(c, T("common.cancel")).OnClick(func() { st.askOld = nil; st.dropJ.err = nil })
				dangerButton(c, T("cleanup.old_go")).OnClick(func() {
					ops := a.cleanup()
					var removed []string
					a.start(&st.dropJ, 2*time.Minute, func(ctx context.Context) error {
						var err error
						removed, err = ops.oldCleanup(ctx, host, e.InstanceID)
						return err
					}, func(err error) {
						if err == nil {
							st.askOld = nil
							delete(st.selOld, e.InstanceID)
							st.note = T("cleanup.old_cleaned", "id", e.InstanceID, "n", len(removed))
							a.readCleanup(host)
							if a.cleanupHook == nil {
								a.refreshAgents()
							}
						}
					})
				})
			})
		})
	}).Dismissed() && !st.dropJ.busy {
		st.askOld = nil
		st.dropJ.err = nil
	}
}

func (a *App) batchCleanupDialog(c *ui.Context, host string, st *cleanupState) {
	sel := cleanupSelection(st, c.Now())
	if sel.count == 0 && !st.dropJ.busy {
		st.askBatch = false
		return
	}
	open := true
	if ui.Modal(c, &open, func() {
		dialog(ui.Column(c), 520).Children(func() {
			dialogTitle(ui.Text(c, T("cleanup.batch_title", "n", sel.count)))
			muted(ui.Text(c, T("cleanup.batch_summary", "prev", len(sel.prevOps), "old", len(sel.oldIDs), "size", size(sel.bytes)))).Wrap()
			ui.Scroll(c).MaxHeight(220).Children(func() {
				ui.Column(c).Gap(4).Padding(10, 12).Radius(10).Background(palette.Board).Children(func() {
					for _, e := range sel.oldItems {
						if total := oldEntryBytes(e); total > 0 || len(e.Leftovers) > 0 {
							mono(muted(ui.Text(c, T("cleanup.batch_item_old", "id", e.InstanceID, "size", size(total))))).Wrap()
						} else {
							mono(muted(ui.Text(c, T("cleanup.batch_item_old_none", "id", e.InstanceID)))).Wrap()
						}
					}
					for _, e := range sel.prevItems {
						mono(muted(ui.Text(c, e.Path+" ("+size(e.Bytes)+")"))).Wrap()
					}
				})
			})
			notice(c, "warn", T("cleanup.batch_warn"))
			if st.dropJ.busy {
				a.jobLine(c, &st.dropJ)
				return
			}
			if st.dropJ.err != nil {
				errorBox(c, st.dropJ.err, a.fold("cleanup-batch-dlg-"+host))
			}
			ui.Row(c).Gap(8).AlignItems(ui.Center).Justify(ui.End).Children(func() {
				needTag(c, sel.need)
				ui.Button(c, T("common.cancel")).OnClick(func() { st.askBatch = false; st.dropJ.err = nil })
				dangerButton(c, T("cleanup.batch_go", "n", sel.count)).OnClick(func() {
					ops := a.cleanup()
					req := service.CleanupBatchRequest{PrevOps: sel.prevOps, OldInstances: sel.oldIDs}
					var res *service.CleanupBatchResult
					a.start(&st.dropJ, 3*time.Minute, func(ctx context.Context) error {
						var err error
						res, err = ops.batch(ctx, host, req)
						return err
					}, func(err error) {
						if err != nil {
							return
						}
						st.askBatch = false
						applyBatchCleanupResult(st, res)
						a.readCleanup(host)
						if res != nil && res.OldDone > 0 && a.cleanupHook == nil {
							a.refreshAgents()
						}
					})
				})
			})
		})
	}).Dismissed() && !st.dropJ.busy {
		st.askBatch = false
		st.dropJ.err = nil
	}
}

func applyBatchCleanupResult(st *cleanupState, res *service.CleanupBatchResult) {
	if res == nil {
		return
	}
	failedPrev := map[string]bool{}
	failedOld := map[string]bool{}
	for _, f := range res.Failures {
		switch f.Kind {
		case "prev":
			failedPrev[f.Target] = true
		case "old":
			failedOld[f.Target] = true
		}
	}
	for op := range st.selPrev {
		if !failedPrev[op] {
			delete(st.selPrev, op)
		}
	}
	for id := range st.selOld {
		if !failedOld[id] {
			delete(st.selOld, id)
		}
	}
	st.batchFails = res.Failures
	doneCount := res.PrevDone + res.OldDone
	if len(res.Failures) > 0 {
		st.note = T("cleanup.batch_partial", "done", doneCount, "removed", len(res.Removed), "failed", len(res.Failures))
		return
	}
	st.note = T("cleanup.batch_done", "done", doneCount, "removed", len(res.Removed))
}
