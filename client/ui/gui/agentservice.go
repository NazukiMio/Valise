package gui

import (
	"context"
	"strings"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

	"valise/core/profile"
	"valise/ui/service"
)

// Overview-tab service control, service installation, and quick profile.actions
// for an enrolled agent (AGENTS.md §7.5: service status, entry port, and command
// exit codes stay strictly separate).

type agentServiceOps struct {
	status  func(context.Context, string, string) (*service.InstanceServiceInfo, error)
	control func(context.Context, string, string, string) (*service.InstanceServiceInfo, error)
	install func(context.Context, string, string, service.InstallIO) (int, *service.InstanceServiceInfo, error)
	actions func(context.Context, string, string) (*service.ActionList, error)
	runAct  func(context.Context, string, string, string, service.ActionOptions) (*service.ActionResult, error)
	access  func(string, profile.Action) string
}

type agentServiceState struct {
	started        bool
	profileVersion int
	read           job
	info           *service.InstanceServiceInfo
	actions        *service.ActionList
	err            error
	ctrlJ          job
	installAsk     bool
	installRun     *serviceTermRun
	actionAsk      *actionModalState
}

type serviceTermRun struct {
	title     string
	running   bool
	wantFocus bool
	term      *terminal.Terminal
	text      tailText
	input     string
	conn      *installConn
	cancel    context.CancelFunc
	code      int
	hasCode   bool
	err       error
}

type actionModalState struct {
	action   profile.Action
	access   string
	plan     string
	seal     bool
	running  bool
	cancel   context.CancelFunc
	progress []string
	termRun  *serviceTermRun
	res      *service.ActionResult
	err      error
}

func (a *App) svcOps() agentServiceOps {
	ops := agentServiceOps{
		status:  a.svc.ServiceStatus,
		control: a.svc.ControlInstanceService,
		install: a.svc.InstallInstanceService,
		actions: a.svc.Actions,
		runAct:  a.svc.RunAction,
		access:  a.svc.ActionAccess,
	}
	if h := a.agentServiceHook; h != nil {
		if h.status != nil {
			ops.status = h.status
		}
		if h.control != nil {
			ops.control = h.control
		}
		if h.install != nil {
			ops.install = h.install
		}
		if h.actions != nil {
			ops.actions = h.actions
		}
		if h.runAct != nil {
			ops.runAct = h.runAct
		}
		if h.access != nil {
			ops.access = h.access
		}
	}
	return ops
}

func (a *App) agentServiceOf(server, instance string) *agentServiceState {
	key := server + "/" + instance
	if a.agentServices == nil {
		a.agentServices = map[string]*agentServiceState{}
	}
	st := a.agentServices[key]
	if st == nil {
		st = &agentServiceState{}
		a.agentServices[key] = st
	}
	if !st.started {
		st.started = true
		a.readAgentService(server, instance)
	}
	return st
}

func (a *App) readAgentService(server, instance string) {
	st := a.agentServices[server+"/"+instance]
	if st == nil || st.read.busy {
		return
	}
	st.started = true
	ops := a.svcOps()
	var info *service.InstanceServiceInfo
	var acts *service.ActionList
	var actErr error
	a.start(&st.read, time.Minute, func(ctx context.Context) error {
		var err error
		info, err = ops.status(ctx, server, instance)
		acts, actErr = ops.actions(ctx, server, instance)
		if err != nil && actErr == nil && acts != nil {
			return nil
		}
		return err
	}, func(err error) {
		st.err = err
		if info != nil {
			st.info = info
			a.syncServiceInfoToAgents(server, instance, info)
		}
		if actErr == nil && acts != nil {
			st.actions = acts
		}
	})
}

func (a *App) agentServiceCard(c *ui.Context, row *agentRow) {
	if row == nil || row.Retired() || row.StateMissing || !a.agentExists(row.Server, row.Instance) {
		return
	}
	st := a.agentServiceOf(row.Server, row.Instance)
	info := st.info
	hasQuick := hasQuickActions(st.actions)
	if !st.read.busy && st.err == nil && info == nil && !hasQuick {
		return
	}
	if info != nil && info.Unit == "" && !hasQuick && !st.read.busy && st.err == nil {
		return
	}
	card(ui.Column(c)).Gap(10).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			cardTitle(ui.Text(c, T("agent.service.title"))).Grow(1)
			if st.read.busy {
				ui.Spinner(c).Size(14, 14)
			}
			quietButton(c, T("common.refresh")).Disabled(st.read.busy || st.ctrlJ.busy).OnClick(func() {
				a.readAgentService(row.Server, row.Instance)
			})
		})
		if st.err != nil {
			errorBox(c, st.err, a.fold("svc-err-"+row.Server+"-"+row.Instance))
		}
		if info != nil && (info.Unit != "" || row.HasService) {
			a.serviceStatusSection(c, row, st, info)
		}
		if hasQuick {
			a.quickActionsSection(c, row, st)
		}
	})
	a.serviceInstallModal(c, row, st)
	a.quickActionModal(c, row, st)
}

func (a *App) serviceStatusSection(c *ui.Context, row *agentRow, st *agentServiceState, info *service.InstanceServiceInfo) {
	tone, label := serviceInfoStatus(info)
	statusRow(c, tone, T("agent.service.status_label"), label)
	if info.Unit != "" {
		fact(c, T("agent.service.unit_label"), info.Unit)
	}
	if info.Stale {
		notice(c, "warn", T("agent.service.stale"))
		a.renderInlineReenroll(c, row, T("adopt.update_profile"), T("native.go_update_profile"))
	}
	if st.ctrlJ.busy {
		a.jobLine(c, &st.ctrlJ)
	}
	if st.ctrlJ.err != nil {
		errorBox(c, st.ctrlJ.err, a.fold("svc-ctrl-err-"+row.Server+"-"+row.Instance))
	}
	switch {
	case info.HasUnit:
		running := info.Active == "active" || info.Active == "activating"
		ui.Row(c).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
			needTag(c, T("need.valise"))
			if !running {
				ui.PrimaryButton(c, T("agent.service.start")).Disabled(st.ctrlJ.busy).OnClick(func() {
					a.runServiceControl(row, st, "start")
				})
			} else {
				ui.Button(c, T("agent.service.start")).Disabled(true)
			}
			ui.Button(c, T("agent.service.restart")).Disabled(st.ctrlJ.busy).OnClick(func() {
				a.runServiceControl(row, st, "restart")
			})
			ui.Button(c, T("agent.service.stop")).Disabled(st.ctrlJ.busy || !running).OnClick(func() {
				a.runServiceControl(row, st, "stop")
			})
		})
	case info.Unit != "":
		if info.LocalOverrideStale {
			notice(c, "warn", T("agent.service.local_override_stale", "id", info.ProfileID, "local", info.EffectiveVersion, "official", info.OfficialVersion))
			ui.Row(c).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
				ui.PrimaryButton(c, T("profiles.restore_official")).Disabled(st.ctrlJ.busy || st.read.busy).OnClick(func() {
					a.restoreOfficialFromService(row, st, info.ProfileID)
				})
				ui.Button(c, T("agent.service.go_profiles")).OnClick(func() {
					a.setTab = 3
					a.navigate("/settings")
				})
			})
		}
		switch {
		case len(info.InstallArgv) > 0 && !info.Stale:
			ui.Row(c).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
				needTag(c, T("need.admin"))
				ui.PrimaryButton(c, T("agent.service.install")).OnClick(func() {
					st.installAsk = true
					st.installRun = nil
				})
			})
		case info.Stale, info.LocalOverrideStale:
		default:
			notice(c, "warn", T("agent.service.no_install_cmd", "unit", info.Unit))
			ui.Row(c).Children(func() {
				ui.Button(c, T("agent.service.go_manage")).OnClick(func() { a.agentTab = tabManage })
			})
		}
	}
}

func (a *App) restoreOfficialFromService(row *agentRow, st *agentServiceState, profileID string) {
	if st.ctrlJ.busy || profileID == "" {
		return
	}
	a.start(&st.ctrlJ, time.Minute, func(context.Context) error {
		return a.svc.RemoveLocalProfile(profileID)
	}, func(err error) {
		if err == nil {
			a.reloadProfiles()
			a.readAgentService(row.Server, row.Instance)
		}
	})
}

func serviceInfoStatus(info *service.InstanceServiceInfo) (string, string) {
	if info == nil || info.Unit == "" {
		return "stop", T("service.none")
	}
	if !info.HasUnit {
		return "stop", T("agent.service.not_installed")
	}
	switch info.Active {
	case "active":
		return "ok", T("service.running")
	case "inactive":
		return "stop", T("service.stopped")
	case "failed":
		return "warn", T("service.failed")
	}
	return "warn", T("service.unknown")
}

func (a *App) runServiceControl(row *agentRow, st *agentServiceState, action string) {
	host, inst := row.Server, row.Instance
	ops := a.svcOps()
	var updated *service.InstanceServiceInfo
	a.start(&st.ctrlJ, 2*time.Minute, func(ctx context.Context) error {
		var err error
		updated, err = ops.control(ctx, host, inst, action)
		return err
	}, func(err error) {
		if err == nil && updated != nil {
			st.info = updated
			a.syncServiceInfoToAgents(host, inst, updated)
			if a.agentServiceHook == nil {
				a.refreshAgents()
			}
		}
	})
}

func (a *App) serviceInstallModal(c *ui.Context, row *agentRow, st *agentServiceState) {
	if !st.installAsk || st.info == nil {
		return
	}
	open := true
	busy := st.installRun != nil && st.installRun.running
	if ui.Modal(c, &open, func() {
		dialog(ui.Column(c), 540).Children(func() {
			dialogTitle(ui.Text(c, T("agent.service.install_title")))
			if st.installRun == nil {
				muted(ui.Text(c, T("agent.service.install_hint"))).Wrap()
				codeBox(mono(ui.Text(c, strings.Join(st.info.InstallArgv, " "))).FontSize(fsCaption).Wrap().Selectable())
				ui.Row(c).Gap(8).AlignItems(ui.Center).Justify(ui.End).Children(func() {
					needTag(c, T("need.admin"))
					ui.Button(c, T("common.cancel")).OnClick(func() { st.installAsk = false })
					ui.PrimaryButton(c, T("agent.service.install_go")).OnClick(func() { a.startServiceInstall(row, st) })
				})
				return
			}
			a.renderServiceTerm(c, st.installRun)
			if st.installRun.running {
				ui.Row(c).Justify(ui.End).Children(func() {
					ui.Button(c, T("agent.term.stop_close")).OnClick(func() {
						stopServiceTerm(st.installRun)
						st.installAsk = false
						st.installRun = nil
					})
				})
			} else {
				if st.installRun.hasCode {
					ui.Text(c, T("agent.service.install_exited", "code", st.installRun.code)).Bold().Selectable()
				}
				if st.info != nil {
					tone, label := serviceInfoStatus(st.info)
					statusRow(c, tone, T("agent.service.after_status"), label)
				}
				if st.installRun.err != nil {
					errorBox(c, st.installRun.err, a.fold("svc-inst-err-"+row.Server+"-"+row.Instance))
				}
				ui.Row(c).Justify(ui.End).Children(func() {
					ui.PrimaryButton(c, T("common.close")).OnClick(func() {
						st.installAsk = false
						st.installRun = nil
					})
				})
			}
		})
	}).Dismissed() && !busy {
		st.installAsk = false
		st.installRun = nil
	}
}

func (a *App) startServiceInstall(row *agentRow, st *agentServiceState) {
	host, inst := row.Server, row.Instance
	ops := a.svcOps()
	conn := newInstallConn()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	tr := &serviceTermRun{title: T("agent.service.install_title"), running: true, wantFocus: true, conn: conn, cancel: cancel}
	tr.term = a.openInstallTerminal(conn, &tr.text)
	st.installRun = tr
	a.run(func() {
		defer cancel()
		code, updated, err := ops.install(ctx, host, inst, service.InstallIO{
			Stdin:    conn.in,
			Stdout:   conn.outW,
			Stderr:   conn.outW,
			PTY:      tr.term != nil,
			OnResize: conn.SetResize,
		})
		conn.Close()
		a.update(func() {
			tr.running, tr.code, tr.hasCode, tr.err = false, code, true, err
			if updated != nil {
				st.info = updated
				a.syncServiceInfoToAgents(host, inst, updated)
			}
			if a.agentServiceHook == nil {
				a.refreshAgents()
			}
		})
	})
}

func (a *App) renderServiceTerm(c *ui.Context, tr *serviceTermRun) {
	a.renderTerminalPaneFocus(c, tr.title, tr.running, tr.term, &tr.wantFocus, &tr.text, &tr.input, tr.conn)
}
