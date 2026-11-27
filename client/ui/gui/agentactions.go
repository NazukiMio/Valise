package gui

import (
	"context"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/profile"
	"valise/ui/service"
)

// Quick profile.actions buttons and confirmation/PTY modal for an agent.

func hasQuickActions(list *service.ActionList) bool {
	if list == nil {
		return false
	}
	for _, act := range list.Actions {
		if act.Output != profile.OutputSecret {
			return true
		}
	}
	return false
}

func (a *App) quickActionsSection(c *ui.Context, row *agentRow, st *agentServiceState) {
	ui.Column(c).Gap(6).Children(func() {
		ui.Text(c, T("agent.actions.title")).FontSize(13).Bold()
		if st.actions.Stale {
			muted(ui.Text(c, T("health.err.stale"))).FontSize(fsSub).Wrap()
		}
		ui.Row(c).Gap(8).Wrap().Children(func() {
			for _, act := range st.actions.Actions {
				if act.Output == profile.OutputSecret {
					continue
				}
				act := act
				ui.Button(c.Key("act-"+act.ID), act.Name).Disabled(st.actions.Stale).OnClick(func() {
					ops := a.svcOps()
					planDigest := ""
					if p := st.actions.Plans[act.ID]; p != nil {
						planDigest = p.Digest
					}
					st.actionAsk = &actionModalState{
						action: act,
						access: ops.access(row.Server, act),
						plan:   planDigest,
						seal:   service.SealsByDefault(act),
					}
				})
			}
		})
	})
}

func (a *App) quickActionModal(c *ui.Context, row *agentRow, st *agentServiceState) {
	m := st.actionAsk
	if m == nil {
		return
	}
	open := true
	busy := m.running || (m.termRun != nil && m.termRun.running)
	if ui.Modal(c, &open, func() {
		dialog(ui.Column(c), 540).Children(func() {
			dialogTitle(ui.Text(c, T("health.run_title", "name", m.action.Name)))
			switch {
			case m.termRun != nil:
				a.renderServiceTerm(c, m.termRun)
				if m.termRun.running {
					ui.Row(c).Justify(ui.End).Children(func() {
						ui.Button(c, T("agent.term.stop_close")).OnClick(func() {
							stopServiceTerm(m.termRun)
							m.termRun = nil
							m.running = false
							st.actionAsk = nil
						})
					})
				} else {
					a.quickActionOutcome(c, row, m)
					ui.Row(c).Justify(ui.End).Children(func() {
						ui.PrimaryButton(c, T("common.close")).OnClick(func() { st.actionAsk = nil })
					})
				}
			case m.running:
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Spinner(c)
					ui.Text(c, T("health.running", "name", m.action.Name))
				})
				for i, p := range m.progress {
					muted(ui.Text(c.Key(i), p))
				}
				ui.Row(c).Justify(ui.End).Children(func() {
					ui.Button(c, T("agent.term.stop_close")).OnClick(func() {
						if m.cancel != nil {
							m.cancel()
						}
						m.running = false
						st.actionAsk = nil
					})
				})
			case m.res != nil || m.err != nil:
				a.quickActionOutcome(c, row, m)
				ui.Row(c).Justify(ui.End).Children(func() {
					ui.PrimaryButton(c, T("common.close")).OnClick(func() { st.actionAsk = nil })
				})
			default:
				muted(ui.Text(c, service.RiskLabels[m.action.Risk]+" · "+m.access)).FontSize(12)
				if m.action.Explain != "" {
					ui.Text(c, m.action.Explain).FontSize(13).Wrap().Selectable()
				}
				if m.action.Undo != "" {
					muted(ui.Text(c, m.action.Undo)).FontSize(12).Wrap().Selectable()
				}
				if len(m.action.Argv) > 0 {
					codeBox(ui.Column(c)).Gap(4).Children(func() {
						muted(ui.Text(c, T("health.run_cmd", "server", row.Server)))
						ui.Text(c, strings.Join(m.action.Argv, " ")).FontSize(12).Font(cur.Fonts.Mono).Wrap().Selectable()
					})
				}
				if m.action.Risk == profile.RiskConfig || m.action.Risk == profile.RiskDanger {
					notice(c, "warn", T("agent.actions.risk_warn"))
					ui.Checkbox(c, &m.seal, T("agent.actions.seal_first"))
				}
				ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
					ui.Button(c, T("common.cancel")).OnClick(func() { st.actionAsk = nil })
					ui.PrimaryButton(c, T("health.run")).OnClick(func() { a.runQuickAction(row, st, m) })
				})
			}
		})
	}).Dismissed() && !busy {
		st.actionAsk = nil
	}
}

func (a *App) quickActionOutcome(c *ui.Context, row *agentRow, m *actionModalState) {
	if m.err != nil {
		errorBox(c, m.err, a.fold("qact-err-"+row.Server+"-"+row.Instance+"-"+m.action.ID))
	}
	if m.res == nil {
		return
	}
	ui.Text(c, T("health.exited", "code", m.res.Exit)).FontSize(13).Bold().Selectable()
	if m.res.Output != "" && m.action.Output != profile.OutputSecret {
		codeBox(mono(ui.Text(c, m.res.Output)).FontSize(fsCaption).Wrap().Selectable())
	}
	if m.res.Checked {
		s, tone := serviceText(m.res.Service)
		statusRow(c, tone, T("agent.service.after_status"), s)
	}
}

func stopServiceTerm(tr *serviceTermRun) {
	if tr == nil {
		return
	}
	tr.running = false
	conn, cancel := tr.conn, tr.cancel
	go func() {
		if conn != nil && conn.inW != nil {
			wrote := make(chan struct{})
			go func() {
				_, _ = conn.inW.Write([]byte{3})
				close(wrote)
			}()
			select {
			case <-wrote:
				time.Sleep(20 * time.Millisecond)
			case <-time.After(80 * time.Millisecond):
			}
		}
		if cancel != nil {
			cancel()
		}
		if conn != nil {
			_ = conn.Close()
		}
	}()
}

func (a *App) runQuickAction(row *agentRow, st *agentServiceState, m *actionModalState) {
	host, inst := row.Server, row.Instance
	actID, seal, planDigest, isTerm := m.action.ID, m.seal, m.plan, m.action.Terminal
	ops := a.svcOps()
	opts := service.ActionOptions{
		Seal:       seal,
		PlanDigest: planDigest,
		Progress:   func(msg string) { a.update(func() { m.progress = append(m.progress, msg) }) },
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	m.cancel = cancel
	var conn *installConn
	if isTerm {
		conn = newInstallConn()
		tr := &serviceTermRun{title: m.action.Name, running: true, wantFocus: true, conn: conn, cancel: cancel}
		tr.term = a.openInstallTerminal(conn, &tr.text)
		m.termRun = tr
		opts.IO = service.InstallIO{
			Stdin:    conn.in,
			Stdout:   conn.outW,
			Stderr:   conn.outW,
			PTY:      tr.term != nil,
			OnResize: conn.SetResize,
		}
	} else {
		m.running = true
	}
	a.run(func() {
		defer cancel()
		res, err := ops.runAct(ctx, host, inst, actID, opts)
		if conn != nil {
			conn.Close()
		}
		a.update(func() {
			m.running, m.res, m.err = false, res, err
			if m.termRun != nil {
				m.termRun.running = false
			}
			a.readAgentService(host, inst)
			if a.agentServiceHook == nil {
				a.refreshAgents()
			}
		})
	})
}
