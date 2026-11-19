package gui

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/revive"
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
	}
}

type cleanupState struct {
	started, busy   bool
	prev            *revive.PrevListResult
	old             *revive.OldListResult
	prevErr, oldErr error
	askPrev         *revive.PrevEntry
	askOld          *revive.OldEntry
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
		})
	})
}

func (a *App) cleanupCard(c *ui.Context, host string, ss *settingsState) {
	st := &ss.cleanup
	card(ui.Column(c)).Gap(8).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, T("cleanup.title")).FontSize(16).Bold().Grow(1)
			if st.busy {
				ui.Spinner(c)
			} else {
				ui.Button(c, T("cleanup.refresh")).OnClick(func() {
					st.note = ""
					a.readCleanup(host)
				})
			}
		})
		muted(ui.Text(c, T("cleanup.what"))).Wrap()
		if st.note != "" {
			ui.Row(c).Gap(10).Padding(10, 14).Radius(10).Background(palette.Board).AlignItems(ui.Center).Children(func() {
				ui.Text(c, st.note).TextColor(palette.Ok).Grow(1).Wrap()
				quietButton(c, T("common.close")).OnClick(func() { st.note = "" })
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
		var prevEntries []revive.PrevEntry
		if st.prev != nil {
			prevEntries = st.prev.Entries
		}
		var oldEntries []revive.OldEntry
		if st.old != nil {
			oldEntries = st.old.Entries
		}
		if st.busy && len(prevEntries) == 0 && len(oldEntries) == 0 {
			muted(ui.Text(c, T("cleanup.loading")))
			return
		}
		if st.started && !st.busy && st.prevErr == nil && st.oldErr == nil && len(prevEntries) == 0 && len(oldEntries) == 0 {
			muted(ui.Text(c, T("cleanup.empty")))
			return
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
	})
	if st.askPrev != nil {
		a.prevDropDialog(c, host, st)
	}
	if st.askOld != nil {
		a.oldCleanupDialog(c, host, st)
	}
}

func (a *App) prevEntryRow(c *ui.Context, st *cleanupState, e revive.PrevEntry) {
	ui.Row(c).Gap(12).Padding(10, 14).Radius(10).Background(palette.Board).AlignItems(ui.Center).Children(func() {
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
	var total int64
	for _, l := range e.Leftovers {
		total += l.Bytes
	}
	whenText := e.Superseded.At.Local().Format("2006-01-02 15:04")
	meta := T("cleanup.old_meta_no_dir", "by", e.Superseded.By, "when", whenText)
	if len(e.Leftovers) > 0 {
		meta = T("cleanup.old_meta", "by", e.Superseded.By, "when", whenText, "size", size(total))
	}
	ui.Row(c).Gap(12).Padding(10, 14).Radius(10).Background(palette.Board).AlignItems(ui.Center).Children(func() {
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

func prevDropNeed(created, now time.Time) string {
	if !created.IsZero() && now.Sub(created) >= cleanupSensitiveAge {
		return T("need.valise")
	}
	return T("need.admin")
}

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
				ui.Button(c, T("common.cancel")).OnClick(func() { st.askPrev = nil })
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
							st.note = T("cleanup.dropped", "n", max(len(removed), 1))
							a.readCleanup(host)
						}
					})
				})
			})
		})
	}).Dismissed() && !st.dropJ.busy {
		st.askPrev = nil
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
				ui.Button(c, T("common.cancel")).OnClick(func() { st.askOld = nil })
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
	}
}
