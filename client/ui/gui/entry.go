package gui

import (
	"context"
	"errors"
	"strconv"

	"github.com/egoist/mygo/ui"

	"valise/core/enroll"
	"valise/ui/service"
)

// entryState is one agent's admin page tunnel while this window lives.
// The page itself opens only in the system browser (docs/UI.md 6).
type entryState struct {
	busy    bool
	open    bool
	display string // token-free address, safe to show
	steps   []service.EntryStatus
	err     error
	cancel  context.CancelFunc
}

var entrySteps = map[string]string{service.EntryTunnel: "entry.step.tunnel", service.EntryRemote: "entry.step.remote",
	service.EntryHealth: "entry.step.health", service.EntryAuth: "entry.step.auth"}

func (a *App) agentEntryList(row *agentRow) []enroll.EntryState {
	for _, it := range a.agents.value.Items {
		if it.ConnectionID == row.Server && it.Instance.InstanceID == row.Instance {
			return it.Instance.Entries
		}
	}
	return nil
}

func (a *App) chosenEntry(row *agentRow, entries []enroll.EntryState) string {
	if len(entries) == 0 {
		return ""
	}
	key := row.Server + "/" + row.Instance
	cur := a.entryChoice[key]
	for _, e := range entries {
		if e.ID == cur {
			return cur
		}
	}
	return entries[0].ID
}

func entryOptionLabel(e enroll.EntryState) string {
	if e.Port > 0 {
		return e.ID + " (:" + strconv.Itoa(e.Port) + ")"
	}
	return e.ID
}

// openEntry starts the tunnel off the main thread; it stays up until it is
// closed, the window closes, or it has been idle for DefaultEntryIdle.
func (a *App) openEntry(server, instance, entryID string) {
	key := server + "/" + instance
	st := &entryState{busy: true}
	a.entries[key] = st
	ctx, cancel := context.WithCancel(context.Background())
	st.cancel = cancel
	a.run(func() {
		err := a.entryOpener(ctx, server, instance, service.OpenOptions{Entry: entryID, Idle: service.DefaultEntryIdle,
			Status: func(s service.EntryStatus) { a.update(func() { st.steps = append(st.steps, s) }) },
			Ready: func(open, display string) {
				a.update(func() { st.busy, st.open, st.display = false, true, display })
				if err := a.openURL(open); err != nil {
					a.update(func() { st.err = errors.New(T("entry.no_browser", "url", display)) })
				}
			}})
		a.update(func() {
			st.busy, st.open = false, false
			if err != nil && ctx.Err() == nil {
				st.err = err
			}
		})
	})
}

func (a *App) closeEntry(key string) {
	if st := a.entries[key]; st != nil && st.cancel != nil {
		st.cancel()
	}
	delete(a.entries, key)
}

// entryButton is the agent page's one primary button and what came of it.
// Controls are emitted directly into agentHeader's Row so no nested full-width
// Row pushes them outside the viewport.
func (a *App) entryButton(c *ui.Context, row *agentRow) {
	key := row.Server + "/" + row.Instance
	st := a.entries[key]
	entries := a.agentEntryList(row)
	chosenID := a.chosenEntry(row, entries)
	if len(entries) > 1 {
		labels := make([]string, len(entries))
		selLabel := ""
		for i, e := range entries {
			labels[i] = entryOptionLabel(e)
			if e.ID == chosenID {
				selLabel = labels[i]
			}
		}
		if ui.Select(c, &selLabel, labels).Width(150).Changed() {
			for i, lbl := range labels {
				if lbl == selLabel {
					if a.entryChoice == nil {
						a.entryChoice = map[string]string{}
					}
					a.entryChoice[key] = entries[i].ID
					chosenID = entries[i].ID
					break
				}
			}
		}
	}
	switch {
	case st == nil:
		ui.PrimaryButton(c, T("entry.open")).OnClick(func() { a.openEntry(row.Server, row.Instance, chosenID) })
	case st.busy:
		ui.Spinner(c)
		muted(ui.Text(c, T("entry.connecting")))
	case st.open:
		ui.Button(c, T("entry.close")).OnClick(func() { a.closeEntry(key) })
	default:
		ui.PrimaryButton(c, T("entry.reopen")).OnClick(func() { a.closeEntry(key); a.openEntry(row.Server, row.Instance, chosenID) })
	}
	quietButton(c, T("forward.open")).OnClick(func() { a.openForwardModal(row) })
}

// entryReport lists each step on its own line: the tunnel, the server's
// port and the page are separate facts (AGENTS.md 7.5).
func (a *App) entryReport(c *ui.Context, row *agentRow) {
	a.forwardBanner(c, row)
	st := a.entries[row.Server+"/"+row.Instance]
	if st == nil {
		return
	}
	reportPanel(ui.Column(c)).Gap(spInside).Children(func() {
		if st.open {
			ui.Text(c, T("entry.opened", "url", st.display)).FontSize(13).Bold()
		}
		remoteFailed := false
		if len(st.steps) > 0 {
			ui.Row(c).Wrap().Gap(spGroup).AlignItems(ui.Center).Children(func() {
				for i, s := range st.steps {
					if s.Step == service.EntryAuth && s.OK {
						continue // the token itself is never shown
					}
					if s.Step == service.EntryRemote && !s.OK {
						remoteFailed = true
					}
					tone := "ok"
					if !s.OK {
						tone = "warn"
					}
					ui.Row(c.Key(i)).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Box(c).Size(6, 6).Radius(3).Background(toneColor(tone))
						ui.Text(c, T(entrySteps[s.Step])).FontSize(12)
						if s.Detail != "" {
							muted(ui.Text(c, s.Detail)).MaxLines(1)
						}
					})
				}
			})
		}
		if remoteFailed {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				quietButton(c, T("forward.open")).OnClick(func() { a.openForwardModal(row) })
			})
		}
		if st.err != nil {
			errorBox(c, st.err, a.fold("entry-err-"+row.Server+row.Instance))
		}
	})
}
