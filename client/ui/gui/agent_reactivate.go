package gui

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/enroll"
	"valise/core/valise"
	"valise/ui/service"
)

// Reactivating a previously migrated-out agent: symmetric switch-back (stopping
// the current active target) or dual-active coexist with explicit conflict
// acknowledgement.

type agentReactivateState struct {
	server             string
	instance           string
	name               string
	targetServer       string
	targetInstance     string
	mode               string
	startService       bool
	confirmBotConflict bool
	localErr           error
	j                  job
}

func (a *App) openAgentReactivate(row *agentRow) {
	a.agentReactivate = &agentReactivateState{
		server:         row.Server,
		instance:       row.Instance,
		name:           orText(row.Name, row.Instance),
		targetServer:   row.MigratedToServer,
		targetInstance: orText(row.MigratedToInstance, row.Instance),
		mode:           enroll.ReactivateModeSwitchBack,
		startService:   true,
	}
}

func (a *App) agentReactivateModal(c *ui.Context, row *agentRow) {
	rs := a.agentReactivate
	if rs == nil || rs.server != row.Server || rs.instance != row.Instance {
		return
	}
	open := true
	if ui.Modal(c, &open, func() {
		dialog(ui.Column(c), 540).Children(func() {
			dialogTitle(ui.Text(c, T("agent.reactivate.title", "name", rs.name)))
			fact(c, T("agent.reactivate.target_fact"), rs.targetServer+" / "+rs.targetInstance)
			if rs.j.busy {
				a.jobLine(c, &rs.j)
				return
			}
			ui.Text(c, T("agent.reactivate.mode_label")).FontSize(13).Bold()
			prevMode := rs.mode
			ui.RadioGroup(c, func() {
				ui.Radio(c.Key("re-sw"), &rs.mode, enroll.ReactivateModeSwitchBack, T("agent.reactivate.mode_switch_back"))
				ui.Radio(c.Key("re-co"), &rs.mode, enroll.ReactivateModeCoexist, T("agent.reactivate.mode_coexist"))
			})
			if rs.mode != prevMode {
				rs.localErr = nil
				rs.j.err = nil
				rs.startService = rs.mode == enroll.ReactivateModeSwitchBack
			}
			if rs.mode == enroll.ReactivateModeSwitchBack {
				subPanel(ui.Column(c)).Gap(6).Children(func() {
					ui.Text(c, T("agent.reactivate.mode_switch_back_hint",
						"server", rs.targetServer,
						"instance", rs.targetInstance)).FontSize(fsSub).Wrap()
				})
			} else {
				ui.Column(c).Gap(8).Padding(12, 14).Radius(10).Background(palette.DangerBg).Border(1, palette.DangerBorder).Children(func() {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Icon(c, icons.alert).FontSize(fsTitle3).TextColor(palette.Danger)
						ui.Text(c, T("agent.reactivate.coexist_warn_title")).Bold().TextColor(palette.Danger)
					})
					ui.Text(c, T("agent.reactivate.coexist_warn_body")).FontSize(fsSub).TextColor(palette.Danger).Wrap()
				})
				ui.Checkbox(c, &rs.confirmBotConflict, T("agent.reactivate.coexist_confirm"))
			}
			ui.Checkbox(c, &rs.startService, T("agent.reactivate.start_service"))
			if rs.localErr != nil {
				errorBox(c, rs.localErr, a.fold("re-local-err-"+rs.server+rs.instance))
			}
			if rs.j.err != nil {
				errorBox(c, rs.j.err, a.fold("re-err-"+rs.server+rs.instance))
			}
			submitLabel := T("agent.reactivate.submit_switch_back")
			if rs.mode == enroll.ReactivateModeCoexist {
				submitLabel = T("agent.reactivate.submit_coexist")
			}
			ui.Row(c).Gap(8).AlignItems(ui.Center).Justify(ui.End).Children(func() {
				needTag(c, T("need.valise"))
				ui.Button(c, T("common.cancel")).OnClick(func() { a.agentReactivate = nil })
				ui.PrimaryButton(c, submitLabel).OnClick(func() { a.submitAgentReactivate(rs) })
			})
		})
	}).Dismissed() && !rs.j.busy {
		a.agentReactivate = nil
	}
}

func (a *App) submitAgentReactivate(rs *agentReactivateState) {
	if rs.mode == enroll.ReactivateModeCoexist && !rs.confirmBotConflict {
		rs.localErr = valise.New(
			"bot_conflict_confirm_required",
			T("agent.reactivate.coexist_need_confirm"),
			T("agent.reactivate.coexist_need_confirm_tip"),
		)
		return
	}
	rs.localErr = nil
	host, inst := rs.server, rs.instance
	req := service.ReactivateAgentRequest{
		ConnectionID:       host,
		InstanceID:         inst,
		Mode:               rs.mode,
		TargetConnectionID: rs.targetServer,
		TargetInstanceID:   rs.targetInstance,
		StartService:       rs.startService,
		ConfirmBotConflict: rs.confirmBotConflict,
	}
	fn := a.reactivateFn
	if fn == nil {
		fn = a.svc.ReactivateAgent
	}
	var res *service.ReactivateAgentResult
	a.start(&rs.j, 2*time.Minute, func(ctx context.Context) error {
		var err error
		res, err = fn(ctx, req)
		return err
	}, func(err error) {
		if err != nil {
			return
		}
		a.agentReactivate = nil
		for i := range a.agents.value.Items {
			it := &a.agents.value.Items[i]
			if it.ConnectionID == host && it.Instance.InstanceID == inst {
				it.MigratedTo = nil
				it.Instance.Superseded = nil
			}
		}
		if res != nil && res.ServiceStatus != nil {
			a.syncServiceInfoToAgents(host, inst, res.ServiceStatus)
		}
		a.readAgentService(host, inst)
		if a.reactivateFn == nil {
			a.refreshAgents()
		}
	})
}
