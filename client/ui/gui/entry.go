package gui

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"valise/core/enroll"
	"valise/ui/service"
)

// entryState is one entry's local SSH tunnel while this window lives.
// The page itself opens only in the system browser (docs/UI.md 6).
type entryState struct {
	entryID       string
	busy          bool
	open          bool
	display       string // token-free address, safe to show
	initialSuffix string // in-memory only; never shown, logged, or persisted
	steps         []service.EntryStatus
	err           error
	cancel        context.CancelFunc
}

var entrySteps = map[string]string{
	service.EntryTunnel: "entry.step.tunnel",
	service.EntryRemote: "entry.step.remote",
	service.EntryHealth: "entry.step.health",
	service.EntryAuth:   "entry.step.auth",
}

func entryKey(server, instance, entryID string) string {
	return server + "/" + instance + "/" + entryID
}

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

func (a *App) entryDisplayName(profileID, entryID string) string {
	if profileID != "" && entryID != "" {
		if name := a.entryDisplay[profileID+"/"+entryID]; name != "" {
			return name
		}
	}
	return entryID
}

func (a *App) entryOptionLabel(profileID string, e enroll.EntryState) string {
	name := e.DisplayName
	if name == "" {
		name = a.entryDisplayName(profileID, e.ID)
	}
	if name == "" {
		name = e.ID
	}
	if e.Port > 0 {
		return name + " (:" + strconv.Itoa(e.Port) + ")"
	}
	return name
}

// openEntry starts the tunnel off the main thread; it stays up until it is
// closed, the window closes, or it has been idle for DefaultEntryIdle.
func (a *App) openEntry(server, instance, entryID, initialSuffix string) {
	key := entryKey(server, instance, entryID)
	if old := a.entries[key]; old != nil {
		old.initialSuffix = ""
		if old.cancel != nil {
			old.cancel()
		}
	}
	st := &entryState{entryID: entryID, busy: true, initialSuffix: initialSuffix}
	a.entries[key] = st
	ctx, cancel := context.WithCancel(context.Background())
	st.cancel = cancel
	a.run(func() {
		err := a.entryOpener(ctx, server, instance, service.OpenOptions{
			Entry:         entryID,
			Idle:          service.DefaultEntryIdle,
			InitialSuffix: initialSuffix,
			Status: func(s service.EntryStatus) {
				a.update(func() { st.steps = append(st.steps, s) })
			},
			Ready: func(open, display string) {
				a.update(func() { st.busy, st.open, st.display = false, true, display })
				if err := a.openURL(open); err != nil {
					a.update(func() { st.err = errors.New(T("entry.no_browser", "url", display)) })
				}
			},
		})
		a.update(func() {
			st.busy, st.open = false, false
			if err != nil && ctx.Err() == nil {
				st.err = err
			}
		})
	})
}

func (a *App) closeEntry(key string) {
	if st := a.entries[key]; st != nil {
		st.initialSuffix = ""
		if st.cancel != nil {
			st.cancel()
		}
	}
	delete(a.entries, key)
}

// entryButton is the agent page's one primary button and entry selector.
// Controls are emitted directly into agentHeader's Row so no nested full-width
// Row pushes them outside the viewport.
func (a *App) entryButton(c *ui.Context, row *agentRow) {
	agentKey := row.Server + "/" + row.Instance
	entries := a.agentEntryList(row)
	chosenID := a.chosenEntry(row, entries)
	if len(entries) > 1 {
		labels := make([]string, len(entries))
		selLabel := ""
		for i, e := range entries {
			labels[i] = a.entryOptionLabel(row.Profile, e)
			if e.ID == chosenID {
				selLabel = labels[i]
			}
		}
		if ui.Select(c, &selLabel, labels).Width(170).Changed() {
			for i, lbl := range labels {
				if lbl == selLabel {
					if a.entryChoice == nil {
						a.entryChoice = map[string]string{}
					}
					a.entryChoice[agentKey] = entries[i].ID
					chosenID = entries[i].ID
					break
				}
			}
		}
	}
	key := entryKey(row.Server, row.Instance, chosenID)
	st := a.entries[key]
	probing := a.isEntryProbing(key)
	switch {
	case probing:
		ui.Spinner(c)
		muted(ui.Text(c, T("entry.probing")))
	case st == nil:
		ui.PrimaryButton(c, T("entry.open")).OnClick(func() { a.requestOpenEntry(row, chosenID) })
	case st.busy:
		ui.Spinner(c)
		muted(ui.Text(c, T("entry.connecting")))
	case st.open:
		ui.Button(c, T("entry.close")).OnClick(func() { a.closeEntry(key) })
	default:
		ui.PrimaryButton(c, T("entry.reopen")).OnClick(func() {
			a.closeEntry(key)
			a.requestOpenEntry(row, chosenID)
		})
	}
	quietButton(c, T("forward.open")).OnClick(func() { a.openForwardModal(row) })
}

type agentEntryReportItem struct {
	key     string
	entryID string
	st      *entryState
	managed bool
}

func (a *App) collectAgentEntryReports(row *agentRow) []agentEntryReportItem {
	prefix := row.Server + "/" + row.Instance + "/"
	legacyKey := row.Server + "/" + row.Instance
	seen := map[string]*agentEntryReportItem{}
	for k, st := range a.entries {
		if k == legacyKey || strings.HasPrefix(k, prefix) {
			eid := st.entryID
			if eid == "" && strings.HasPrefix(k, prefix) {
				eid = strings.TrimPrefix(k, prefix)
			}
			seen[k] = &agentEntryReportItem{key: k, entryID: eid, st: st, managed: a.isEntryManaged(k)}
		}
	}
	for k, managed := range a.entryManaged {
		if !managed || !strings.HasPrefix(k, prefix) {
			continue
		}
		if it, ok := seen[k]; ok {
			it.managed = true
			continue
		}
		seen[k] = &agentEntryReportItem{key: k, entryID: strings.TrimPrefix(k, prefix), managed: true}
	}
	out := make([]agentEntryReportItem, 0, len(seen))
	for _, it := range seen {
		out = append(out, *it)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key < out[j].key })
	return out
}

// entryReport lists each active/errored entry tunnel and managed remote panel
// independently (AGENTS.md 7.5).
func (a *App) entryReport(c *ui.Context, row *agentRow) {
	a.forwardBanner(c, row)
	items := a.collectAgentEntryReports(row)
	for _, it := range items {
		a.renderEntryReportItem(c, row, it)
	}
}

func (a *App) renderEntryReportItem(c *ui.Context, row *agentRow, it agentEntryReportItem) {
	st := it.st
	stopJ := a.entryStopJobs[it.key]
	label := a.entryDisplayName(row.Profile, it.entryID)
	reportPanel(ui.Column(c.Key("entry-rep-" + it.key))).Gap(spInside).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Wrap().Children(func() {
			if label != "" {
				ui.Text(c, label).FontSize(13).Bold()
			}
			switch {
			case st != nil && st.open:
				ui.Text(c, T("entry.opened", "url", st.display)).FontSize(13).Bold().Grow(1)
			case st != nil && st.busy:
				ui.Spinner(c).Size(14, 14)
				muted(ui.Text(c, T("entry.connecting"))).Grow(1)
			case it.managed:
				muted(ui.Text(c, T("entry.remote_managed"))).FontSize(fsSub).Grow(1)
			}
			if st != nil && (st.open || st.busy) {
				ui.Button(c.Key("close-tun-"+it.key), T("entry.close_tunnel")).OnClick(func() {
					a.closeEntry(it.key)
				})
			}
			if it.managed {
				if stopJ != nil && stopJ.busy {
					ui.Spinner(c).Size(14, 14)
					muted(ui.Text(c, T("entry.stopping_remote")))
				} else {
					needTag(c, T("need.valise"))
					ui.Button(c.Key("stop-rem-"+it.key), T("entry.stop_remote")).OnClick(func() {
						a.stopRemoteEntry(row, it.entryID)
					})
				}
			}
		})
		if st != nil && st.open && it.managed {
			muted(ui.Text(c, T("entry.remote_managed"))).FontSize(fsCaption)
		}
		remoteFailed := false
		if st != nil && len(st.steps) > 0 {
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
		if st != nil && st.err != nil {
			errorBox(c, st.err, a.fold("entry-err-"+it.key))
		}
		if stopJ != nil && stopJ.err != nil {
			errorBox(c, stopJ.err, a.fold("entry-stop-err-"+it.key))
		}
	})
}
