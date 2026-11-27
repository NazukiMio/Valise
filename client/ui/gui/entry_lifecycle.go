package gui

import (
	"context"
	"strconv"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/login"
)

// On-demand entry probing, prepare/background-start confirmation modal, and
// independent remote panel stop (docs/LOGIN_RELAY.md 4.3).

type entryLifecycleState struct {
	server     string
	instance   string
	entryID    string
	entryLabel string
	probe      *login.EntryProbeResult
	phase      string
	j          job
}

func (a *App) isEntryProbing(key string) bool {
	j := a.entryProbeJobs[key]
	return j != nil && j.busy
}

func (a *App) isEntryManaged(key string) bool {
	return a.entryManaged != nil && a.entryManaged[key]
}

func (a *App) setEntryManaged(key string, managed bool) {
	if a.entryManaged == nil {
		a.entryManaged = map[string]bool{}
	}
	if managed {
		a.entryManaged[key] = true
	} else {
		delete(a.entryManaged, key)
	}
}

func (a *App) probeEntryFn() func(context.Context, string, string, string) (*login.EntryProbeResult, error) {
	if a.entryProber != nil {
		return a.entryProber
	}
	return a.svc.ProbeEntry
}

func (a *App) prepareEntryFn() func(context.Context, string, string, string) (*login.EntryPrepareResult, error) {
	if a.entryPreparer != nil {
		return a.entryPreparer
	}
	return a.svc.PrepareEntry
}

func (a *App) startEntryFn() func(context.Context, string, string, string) (*login.EntryStartResult, error) {
	if a.entryStarter != nil {
		return a.entryStarter
	}
	return a.svc.StartEntry
}

func (a *App) stopEntryFn() func(context.Context, string, string, string) (*login.EntryStopResult, error) {
	if a.entryStopper != nil {
		return a.entryStopper
	}
	return a.svc.StopEntry
}

// requestOpenEntry probes the entry first. If already listening (or if probe
// fails on an unsupported/offline target), it opens the tunnel directly;
// otherwise it opens the lifecycle confirmation modal.
func (a *App) requestOpenEntry(row *agentRow, entryID string) {
	key := entryKey(row.Server, row.Instance, entryID)
	if a.entryProbeJobs == nil {
		a.entryProbeJobs = map[string]*job{}
	}
	pj := a.entryProbeJobs[key]
	if pj == nil {
		pj = &job{}
		a.entryProbeJobs[key] = pj
	}
	if pj.busy {
		return
	}
	host, inst, prof := row.Server, row.Instance, row.Profile
	fn := a.probeEntryFn()
	var res *login.EntryProbeResult
	a.start(pj, 30*time.Second, func(ctx context.Context) error {
		var err error
		res, err = fn(ctx, host, inst, entryID)
		return err
	}, func(err error) {
		if err != nil || res == nil {
			a.openEntry(host, inst, entryID, "")
			return
		}
		a.setEntryManaged(key, res.ManagedAlive)
		if res.Listening {
			a.openEntry(host, inst, entryID, "")
			return
		}
		label := a.entryDisplayName(prof, res.EntryID)
		if label == "" {
			label = orText(res.EntryID, entryID)
		}
		a.entryLifecycle = &entryLifecycleState{
			server:     host,
			instance:   inst,
			entryID:    orText(res.EntryID, entryID),
			entryLabel: label,
			probe:      res,
		}
	})
}

func (a *App) entryLifecycleModal(c *ui.Context, row *agentRow) {
	m := a.entryLifecycle
	if m == nil || m.server != row.Server || m.instance != row.Instance || m.probe == nil {
		return
	}
	p := m.probe
	open := true
	if ui.Modal(c, &open, func() {
		dialog(ui.Column(c), 500).Children(func() {
			dialogTitle(ui.Text(c, T("entry.lifecycle.title", "entry", m.entryLabel)))
			fact(c, T("entry.lifecycle.entry_label"), m.entryID)
			if p.Port > 0 {
				fact(c, T("entry.lifecycle.port_label"), strconv.Itoa(p.Port))
			}
			notice(c, "warn", T("entry.lifecycle.not_listening", "port", p.Port))
			if m.j.busy {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Spinner(c).Size(14, 14)
					muted(ui.Text(c, m.phase))
				})
				return
			}
			if m.j.err != nil {
				errorBox(c, m.j.err, a.fold("entry-lc-err-"+m.server+m.instance+m.entryID))
			}
			switch {
			case p.HasPrepare || p.HasStart:
				needsPrepare := !p.Prepared && p.HasPrepare
				if needsPrepare {
					ui.Text(c, T("entry.lifecycle.prepare_needed")).FontSize(fsSub).Wrap()
					if p.PrepareNote != "" {
						fact(c, T("entry.lifecycle.prepare_note_label"), p.PrepareNote)
					}
				} else {
					ui.Text(c, T("entry.lifecycle.start_hint")).FontSize(fsSub).Wrap()
				}
				ui.Row(c).Gap(8).AlignItems(ui.Center).Wrap().Justify(ui.End).Children(func() {
					if needsPrepare {
						needTag(c, T("need.admin"))
					}
					if p.HasStart {
						needTag(c, T("need.valise"))
					}
					ui.Button(c, T("common.cancel")).OnClick(func() { a.entryLifecycle = nil })
					quietButton(c, T("entry.lifecycle.try_forward")).OnClick(func() {
						host, inst, eid := m.server, m.instance, m.entryID
						a.entryLifecycle = nil
						a.openEntry(host, inst, eid, "")
					})
					if needsPrepare {
						ui.PrimaryButton(c, T("entry.lifecycle.prepare_and_start")).OnClick(func() {
							a.runEntryPrepareAndStart(m)
						})
					} else {
						ui.PrimaryButton(c, T("entry.lifecycle.start_and_open")).OnClick(func() {
							a.runEntryStart(m)
						})
					}
				})
			default:
				ui.Text(c, T("entry.lifecycle.manual_hint", "port", p.Port)).FontSize(fsSub).Wrap()
				if p.AgentHint != "" {
					subPanel(ui.Column(c)).Gap(4).Children(func() {
						ui.Text(c, T("entry.lifecycle.agent_hint_title")).FontSize(fsSub).Bold().Wrap()
						mono(ui.Text(c, p.AgentHint)).FontSize(fsSub).Wrap().Selectable()
					})
				} else if p.LocalSource == "agent_dossier" {
					muted(ui.Text(c, T("entry.lifecycle.agent_dossier_note"))).FontSize(fsSub).Wrap()
				}
				ui.Row(c).Gap(8).AlignItems(ui.Center).Wrap().Justify(ui.End).Children(func() {
					if p.LocalSource == "agent_dossier" {
						needTag(c, T("need.valise"))
					}
					ui.Button(c, T("common.cancel")).OnClick(func() { a.entryLifecycle = nil })
					if p.AgentHint != "" || p.LocalSource == "agent_dossier" {
						ui.Button(c, T("agent.server_shell")).OnClick(func() {
							srv, inst := m.server, m.instance
							a.entryLifecycle = nil
							a.openServerShellFromAgent(srv, inst)
						})
					} else {
						ui.Button(c, T("forward.open")).OnClick(func() {
							a.entryLifecycle = nil
							a.openForwardModal(row)
						})
					}
					if p.LocalSource == "agent_dossier" {
						quietButton(c, T("entry.lifecycle.remove_local")).OnClick(func() {
							a.runRemoveLocalEntry(m)
						})
					}
					ui.PrimaryButton(c, T("entry.lifecycle.try_forward")).OnClick(func() {
						host, inst, eid := m.server, m.instance, m.entryID
						a.entryLifecycle = nil
						a.openEntry(host, inst, eid, "")
					})
				})
			}
		})
	}).Dismissed() && !m.j.busy {
		a.entryLifecycle = nil
	}
}

func (a *App) runEntryPrepareAndStart(m *entryLifecycleState) {
	host, inst, eid := m.server, m.instance, m.entryID
	hasStart := m.probe.HasStart
	prepFn := a.prepareEntryFn()
	startFn := a.startEntryFn()
	m.phase = T("entry.lifecycle.phase_prepare")
	var startRes *login.EntryStartResult
	a.start(&m.j, 5*time.Minute, func(ctx context.Context) error {
		prepRes, err := prepFn(ctx, host, inst, eid)
		if err != nil {
			return err
		}
		if hasStart && (prepRes == nil || !prepRes.Listening) {
			a.update(func() { m.phase = T("entry.lifecycle.phase_start") })
			startRes, err = startFn(ctx, host, inst, eid)
			return err
		}
		return nil
	}, func(err error) {
		if err != nil {
			return
		}
		authSuffix := ""
		if startRes != nil {
			if startRes.ManagedByUs {
				a.setEntryManaged(entryKey(host, inst, eid), true)
			}
			authSuffix = startRes.AuthSuffix
		}
		a.entryLifecycle = nil
		a.openEntry(host, inst, eid, authSuffix)
	})
}

func (a *App) runEntryStart(m *entryLifecycleState) {
	host, inst, eid := m.server, m.instance, m.entryID
	startFn := a.startEntryFn()
	m.phase = T("entry.lifecycle.phase_start")
	var startRes *login.EntryStartResult
	a.start(&m.j, 2*time.Minute, func(ctx context.Context) error {
		var err error
		startRes, err = startFn(ctx, host, inst, eid)
		return err
	}, func(err error) {
		if err != nil {
			return
		}
		authSuffix := ""
		if startRes != nil {
			if startRes.ManagedByUs {
				a.setEntryManaged(entryKey(host, inst, eid), true)
			}
			authSuffix = startRes.AuthSuffix
		}
		a.entryLifecycle = nil
		a.openEntry(host, inst, eid, authSuffix)
	})
}

func (a *App) stopRemoteEntry(row *agentRow, entryID string) {
	host, inst := row.Server, row.Instance
	key := entryKey(host, inst, entryID)
	if st := a.entries[key]; st != nil {
		st.initialSuffix = ""
	}
	if a.entryStopJobs == nil {
		a.entryStopJobs = map[string]*job{}
	}
	sj := a.entryStopJobs[key]
	if sj == nil {
		sj = &job{}
		a.entryStopJobs[key] = sj
	}
	if sj.busy {
		return
	}
	stopFn := a.stopEntryFn()
	var res *login.EntryStopResult
	a.start(sj, time.Minute, func(ctx context.Context) error {
		var err error
		res, err = stopFn(ctx, host, inst, entryID)
		return err
	}, func(err error) {
		if err == nil && (res == nil || res.Stopped || !res.Listening) {
			a.setEntryManaged(key, false)
			if st := a.entries[key]; st != nil {
				st.initialSuffix = ""
			}
		}
	})
}

func (a *App) runRemoveLocalEntry(m *entryLifecycleState) {
	if m == nil {
		return
	}
	host, inst, eid := m.server, m.instance, m.entryID
	fn := a.removeLocalEntryFn
	if fn == nil {
		fn = a.svc.RemoveLocalEntry
	}
	m.phase = T("entry.lifecycle.phase_remove")
	a.start(&m.j, time.Minute, func(ctx context.Context) error {
		return fn(ctx, host, inst, eid)
	}, func(err error) {
		if err != nil {
			return
		}
		for i := range a.agents.value.Items {
			it := &a.agents.value.Items[i]
			if it.ConnectionID == host && it.Instance.InstanceID == inst {
				kept := it.Instance.Entries[:0]
				for _, en := range it.Instance.Entries {
					if en.ID != eid {
						kept = append(kept, en)
					}
				}
				it.Instance.Entries = kept
			}
		}
		a.entryLifecycle = nil
		if a.removeLocalEntryFn == nil {
			a.refreshAgents()
		}
	})
}
