package gui

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/revive"
	"valise/ui/service"
)

// Server space cleanup (docs/UI.md 9.3, ARCHITECTURE.md 8.2.1): kept pre-swap
// directories (.valise-prev-*) and superseded old instances. Every deletion is
// explicitly confirmed by the user; vault backups are untouched.

const cleanupSensitiveAge = 7 * 24 * time.Hour

type cleanupOps struct {
	prevList   func(context.Context, string) (*revive.PrevListResult, error)
	prevDrop   func(context.Context, string, string) ([]string, error)
	oldList    func(context.Context, string) (*revive.OldListResult, error)
	oldCleanup func(context.Context, string, string) ([]string, error)
	batch      func(context.Context, string, service.CleanupBatchRequest) (*service.CleanupBatchResult, error)
}

func (a *App) cleanup() cleanupOps {
	if a.cleanupHook != nil {
		return *a.cleanupHook
	}
	return cleanupOps{
		prevList:   a.svc.PrevList,
		prevDrop:   a.svc.PrevDrop,
		oldList:    a.svc.OldList,
		oldCleanup: a.svc.OldCleanup,
		batch:      a.svc.CleanupBatch,
	}
}

type cleanupState struct {
	started, busy   bool
	prev            *revive.PrevListResult
	old             *revive.OldListResult
	prevErr, oldErr error
	selPrev         map[string]bool
	selOld          map[string]bool
	askPrev         *revive.PrevEntry
	askOld          *revive.OldEntry
	askBatch        bool
	batchFails      []service.CleanupBatchFailure
	dropJ           job
	note            string
}

func (a *App) readCleanup(host string) {
	ss := a.settingsOf(host)
	if ss.cleanup.busy {
		return
	}
	ops := a.cleanup()
	ss.cleanup.started, ss.cleanup.busy = true, true
	ss.cleanup.prevErr, ss.cleanup.oldErr = nil, nil
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		prev, prevErr := ops.prevList(ctx, host)
		old, oldErr := ops.oldList(ctx, host)
		a.update(func() {
			ss.cleanup.busy = false
			ss.cleanup.prev, ss.cleanup.prevErr = prev, prevErr
			ss.cleanup.old, ss.cleanup.oldErr = old, oldErr
			pruneCleanupSelection(&ss.cleanup)
		})
	})
}

func pruneCleanupSelection(st *cleanupState) {
	prevEntries, oldEntries := cleanupEntries(st)
	if len(st.selPrev) > 0 {
		have := map[string]bool{}
		for _, e := range prevEntries {
			have[e.OpID] = true
		}
		for op := range st.selPrev {
			if !have[op] {
				delete(st.selPrev, op)
			}
		}
	}
	if len(st.selOld) > 0 {
		have := map[string]bool{}
		for _, e := range oldEntries {
			have[e.InstanceID] = true
		}
		for id := range st.selOld {
			if !have[id] {
				delete(st.selOld, id)
			}
		}
	}
}

func cleanupEntries(st *cleanupState) ([]revive.PrevEntry, []revive.OldEntry) {
	var prev []revive.PrevEntry
	if st.prev != nil {
		prev = st.prev.Entries
	}
	var old []revive.OldEntry
	if st.old != nil {
		old = st.old.Entries
	}
	return prev, old
}

type cleanupSel struct {
	prevOps     []string
	oldIDs      []string
	prevItems   []revive.PrevEntry
	oldItems    []revive.OldEntry
	count       int
	allSelected bool
	bytes       int64
	need        string
}

func cleanupSelection(st *cleanupState, now time.Time) cleanupSel {
	prevEntries, oldEntries := cleanupEntries(st)
	var out cleanupSel
	allPrev := map[string]bool{}
	seenPrevOp := map[string]bool{}
	seenPath := map[string]bool{}
	needAdmin := false
	for _, e := range prevEntries {
		allPrev[e.OpID] = true
		if !st.selPrev[e.OpID] {
			continue
		}
		out.prevItems = append(out.prevItems, e)
		if !seenPrevOp[e.OpID] {
			seenPrevOp[e.OpID] = true
			out.prevOps = append(out.prevOps, e.OpID)
		}
		if e.Path == "" || !seenPath[e.Path] {
			if e.Path != "" {
				seenPath[e.Path] = true
			}
			out.bytes += e.Bytes
		}
		if prevDropNeed(e.Created, now) == T("need.admin") {
			needAdmin = true
		}
	}
	allOld := map[string]bool{}
	seenOld := map[string]bool{}
	for _, e := range oldEntries {
		allOld[e.InstanceID] = true
		if !st.selOld[e.InstanceID] {
			continue
		}
		needAdmin = true
		out.oldItems = append(out.oldItems, e)
		if !seenOld[e.InstanceID] {
			seenOld[e.InstanceID] = true
			out.oldIDs = append(out.oldIDs, e.InstanceID)
		}
		for _, l := range e.Leftovers {
			if l.Path == "" || !seenPath[l.Path] {
				if l.Path != "" {
					seenPath[l.Path] = true
				}
				out.bytes += l.Bytes
			}
		}
	}
	out.count = len(out.prevOps) + len(out.oldIDs)
	totalSelectable := len(allPrev) + len(allOld)
	out.allSelected = totalSelectable > 0 && out.count == totalSelectable
	if needAdmin {
		out.need = T("need.admin")
	} else {
		out.need = T("need.valise")
	}
	return out
}

func (a *App) cleanupCard(c *ui.Context, host string, ss *settingsState) {
	st := &ss.cleanup
	prevEntries, oldEntries := cleanupEntries(st)
	card(ui.Column(c)).Gap(8).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, T("cleanup.title")).FontSize(16).Bold().Grow(1)
			if st.busy {
				ui.Spinner(c)
			} else {
				ui.Button(c, T("cleanup.refresh")).OnClick(func() {
					st.note = ""
					st.batchFails = nil
					a.readCleanup(host)
				})
			}
		})
		muted(ui.Text(c, T("cleanup.what"))).Wrap()
		if st.note != "" {
			ui.Row(c).Gap(10).Padding(10, 14).Radius(10).Background(palette.Board).AlignItems(ui.Center).Children(func() {
				ui.Text(c, st.note).TextColor(palette.Ok).Grow(1).Wrap()
				quietButton(c, T("common.close")).OnClick(func() { st.note = ""; st.batchFails = nil })
			})
		}
		if len(st.batchFails) > 0 {
			subPanel(ui.Column(c)).Gap(4).Children(func() {
				ui.Text(c, T("cleanup.batch_fails_title")).TextColor(palette.Warn).Bold()
				for _, f := range st.batchFails {
					mono(muted(ui.Text(c, T("cleanup.batch_fail_item", "target", f.Target, "reason", reasonOf(f.Err))))).Wrap()
				}
			})
		}
		if st.prevErr != nil {
			errorBox(c, st.prevErr, a.fold("cleanup-prev-err-"+host))
		}
		if st.oldErr != nil {
			errorBox(c, st.oldErr, a.fold("cleanup-old-err-"+host))
		}
		if st.dropJ.err != nil {
			errorBox(c, st.dropJ.err, a.fold("cleanup-drop-err-"+host))
		}
		if st.busy && len(prevEntries) == 0 && len(oldEntries) == 0 {
			muted(ui.Text(c, T("cleanup.loading")))
			a.missingInstancesCleanupSection(c, host)
			return
		}
		if st.started && !st.busy && st.prevErr == nil && st.oldErr == nil && len(prevEntries) == 0 && len(oldEntries) == 0 {
			muted(ui.Text(c, T("cleanup.empty")))
			a.missingInstancesCleanupSection(c, host)
			return
		}
		if len(prevEntries) > 0 || len(oldEntries) > 0 {
			a.cleanupBatchBar(c, st, prevEntries, oldEntries)
		}
		if len(prevEntries) > 0 {
			ui.Text(c, T("cleanup.prev_title")).FontSize(13).Bold().Padding(4, 0, 0, 0)
			for _, e := range prevEntries {
				a.prevEntryRow(c.Key("prev-"+e.OpID+"-"+e.Path), st, e)
			}
		}
		if len(oldEntries) > 0 {
			ui.Text(c, T("cleanup.old_title")).FontSize(13).Bold().Padding(4, 0, 0, 0)
			for _, e := range oldEntries {
				a.oldEntryRow(c.Key("old-"+e.InstanceID), st, e)
			}
		}
		a.missingInstancesCleanupSection(c, host)
	})
	a.agentForgetModal(c)
	if st.askPrev != nil {
		a.prevDropDialog(c, host, st)
	}
	if st.askOld != nil {
		a.oldCleanupDialog(c, host, st)
	}
	if st.askBatch {
		a.batchCleanupDialog(c, host, st)
	}
}

func (a *App) cleanupBatchBar(c *ui.Context, st *cleanupState, prevEntries []revive.PrevEntry, oldEntries []revive.OldEntry) {
	sel := cleanupSelection(st, c.Now())
	ui.Row(c).Gap(8).Padding(8, 12).Radius(10).Background(palette.Board).AlignItems(ui.Center).Wrap().Children(func() {
		if sel.allSelected {
			ui.Button(c, T("cleanup.clear_sel")).Disabled(st.dropJ.busy).OnClick(func() {
				st.selPrev, st.selOld = nil, nil
			})
		} else {
			ui.Button(c, T("cleanup.select_all")).Disabled(st.dropJ.busy).OnClick(func() {
				st.selPrev = map[string]bool{}
				for _, e := range prevEntries {
					st.selPrev[e.OpID] = true
				}
				st.selOld = map[string]bool{}
				for _, e := range oldEntries {
					st.selOld[e.InstanceID] = true
				}
			})
		}
		if sel.count > 0 {
			muted(ui.Text(c, T("cleanup.selected_summary", "n", sel.count, "size", size(sel.bytes)))).Grow(1).Wrap()
			needTag(c, sel.need)
			ui.Button(c, T("cleanup.batch_open", "n", sel.count)).Disabled(st.dropJ.busy).OnClick(func() {
				st.askBatch = true
				st.dropJ.err = nil
			})
		} else {
			muted(ui.Text(c, T("cleanup.batch_hint"))).Grow(1).Wrap()
			ui.Button(c, T("cleanup.batch_open_none")).Disabled(true)
		}
	})
}

func (a *App) prevEntryRow(c *ui.Context, st *cleanupState, e revive.PrevEntry) {
	ui.Row(c).Gap(12).Padding(10, 14).Radius(10).Background(palette.Board).AlignItems(ui.Center).Children(func() {
		checked := st.selPrev[e.OpID]
		if ui.Checkbox(c.Key("cb-prev-"+e.OpID+"-"+e.Path), &checked, "").Label(T("cleanup.pick_prev", "path", e.Path)).Changed() {
			if st.selPrev == nil {
				st.selPrev = map[string]bool{}
			}
			if checked {
				st.selPrev[e.OpID] = true
			} else {
				delete(st.selPrev, e.OpID)
			}
		}
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			mono(ui.Text(c, e.Path)).Wrap()
			muted(ui.Text(c, T("cleanup.prev_meta", "when", e.Created.Local().Format("2006-01-02 15:04"), "size", size(e.Bytes)))).Wrap()
		})
		actionWith(c, T("cleanup.drop_open"), prevDropNeed(e.Created, c.Now()), func() {
			e := e
			st.askPrev, st.dropJ.err = &e, nil
		})
	})
}

func (a *App) oldEntryRow(c *ui.Context, st *cleanupState, e revive.OldEntry) {
	total := oldEntryBytes(e)
	whenText := e.Superseded.At.Local().Format("2006-01-02 15:04")
	meta := T("cleanup.old_meta_no_dir", "by", e.Superseded.By, "when", whenText)
	if len(e.Leftovers) > 0 {
		meta = T("cleanup.old_meta", "by", e.Superseded.By, "when", whenText, "size", size(total))
	}
	ui.Row(c).Gap(12).Padding(10, 14).Radius(10).Background(palette.Board).AlignItems(ui.Center).Children(func() {
		checked := st.selOld[e.InstanceID]
		if ui.Checkbox(c.Key("cb-old-"+e.InstanceID), &checked, "").Label(T("cleanup.pick_old", "id", e.InstanceID)).Changed() {
			if st.selOld == nil {
				st.selOld = map[string]bool{}
			}
			if checked {
				st.selOld[e.InstanceID] = true
			} else {
				delete(st.selOld, e.InstanceID)
			}
		}
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			ui.Text(c, e.InstanceID).Bold()
			muted(ui.Text(c, meta)).Wrap()
		})
		actionWith(c, T("cleanup.old_open"), T("need.admin"), func() {
			e := e
			st.askOld, st.dropJ.err = &e, nil
		})
	})
}

func oldEntryBytes(e revive.OldEntry) int64 {
	var total int64
	for _, l := range e.Leftovers {
		total += l.Bytes
	}
	return total
}

func prevDropNeed(created, now time.Time) string {
	if !created.IsZero() && now.Sub(created) >= cleanupSensitiveAge {
		return T("need.valise")
	}
	return T("need.admin")
}
