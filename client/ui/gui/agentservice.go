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
	started    bool
	read       job
	info       *service.InstanceServiceInfo
	actions    *service.ActionList
	err        error
	ctrlJ      job
	installAsk bool
	installRun *serviceTermRun
	actionAsk  *actionModalState
}

type serviceTermRun struct {
	title   string
	running bool
	term    *terminal.Terminal
	text    tailText
	input   string
	conn    *installConn
	code    int
	hasCode bool
	err     error
}

type actionModalState struct {
	action   profile.Action
	access   string
	plan     string
	seal     bool
	running  bool
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
		ui.Row(c).Children(func() {
			ui.Button(c, T("native.go_update_profile")).OnClick(func() { a.router.Push("/servers/" + row.Server + "/agents") })
		})
	}
	if st.ctrlJ.busy {
		a.jobLine(c, &st.ctrlJ)
	}
	if st.ctrlJ.err != nil {
		errorBox(c, st.ctrlJ.err, a.fold("svc-ctrl-err-"+row.Server+"-"+row.Instance))
	}
	switch {
	case info.HasUnit:
		ui.Row(c).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
			needTag(c, T("need.valise"))
			if info.Active != "active" {
				ui.PrimaryButton(c, T("agent.service.start")).Disabled(st.ctrlJ.busy).OnClick(func() {
					a.runServiceControl(row, st, "start")
				})
			}
			ui.Button(c, T("agent.service.restart")).Disabled(st.ctrlJ.busy).OnClick(func() {
				a.runServiceControl(row, st, "restart")
			})
			if info.Active == "active" || info.Active == "activating" {
				ui.Button(c, T("agent.service.stop")).Disabled(st.ctrlJ.busy).OnClick(func() {
					a.runServiceControl(row, st, "stop")
				})
			}
		})
	case info.Unit != "" && len(info.InstallArgv) > 0 && !info.Stale:
		ui.Row(c).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
			needTag(c, T("need.admin"))
			ui.PrimaryButton(c, T("agent.service.install")).OnClick(func() {
				st.installAsk = true
				st.installRun = nil
			})
		})
	}
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
			if !st.installRun.running {
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
	tr := &serviceTermRun{title: T("agent.service.install_title"), running: true, conn: conn}
	tr.term = a.openInstallTerminal(conn, &tr.text)
	st.installRun = tr
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
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
			}
			if a.agentServiceHook == nil {
				a.refreshAgents()
			}
		})
	})
}

func (a *App) renderServiceTerm(c *ui.Context, tr *serviceTermRun) {
	a.renderTerminalPane(c, tr.title, tr.running, tr.term, &tr.text, &tr.input, tr.conn)
}
