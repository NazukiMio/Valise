package gui

import (
	"context"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/core/enroll"
	"valise/ui/service"
)

// Forget an instance (removing Valise metadata on the server and local client
// caches without touching agent files on disk or backup snapshots).

type agentForgetState struct {
	server   string
	instance string
	name     string
	j        job
}

func (a *App) openAgentForget(server, instance, name string) {
	a.agentForget = &agentForgetState{
		server:   server,
		instance: instance,
		name:     orText(name, instance),
	}
}

func (a *App) agentForgetBanner(c *ui.Context, row *agentRow) {
	if row == nil || !row.StateMissing || row.Retired() {
		return
	}
	ui.Column(c).Gap(8).Padding(14, 16).Radius(10).Background(palette.DangerBg).Border(1, palette.DangerBorder).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icons.alert).FontSize(fsTitle3).TextColor(palette.Danger)
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				ui.Text(c, T("agent.forget.missing_banner")).Bold().TextColor(palette.Danger).Wrap().Selectable()
				if len(row.MissingRoots) > 0 {
					mono(muted(ui.Text(c, T("agent.forget.missing_roots", "roots", strings.Join(row.MissingRoots, ", "))))).Wrap().Selectable()
				}
			})
			ui.Button(c, T("agent.forget.open")).OnClick(func() {
				a.openAgentForget(row.Server, row.Instance, row.Name)
			})
		})
	})
}

func (a *App) agentForgetManageRow(c *ui.Context, row *agentRow) {
	if row == nil {
		return
	}
	card(ui.Row(c)).Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Gap(4).Children(func() {
			ui.Text(c, T("agent.forget.card_title")).Bold()
			muted(ui.Text(c, T("agent.forget.card_hint"))).FontSize(fsSub).Wrap()
		})
		actionWith(c, T("agent.forget.open"), T("need.valise"), func() {
			a.openAgentForget(row.Server, row.Instance, row.Name)
		})
	})
}

func (a *App) missingInstancesCleanupSection(c *ui.Context, host string) {
	var missing []service.AgentSummary
	for _, it := range a.agents.value.Items {
		if it.ConnectionID == host && it.MigratedTo == nil && it.Instance.Superseded == nil && it.Instance.StateMissing() {
			missing = append(missing, it)
		}
	}
	if len(missing) == 0 {
		return
	}
	ui.Column(c).Gap(6).Children(func() {
		ui.Text(c, T("agent.forget.cleanup_title")).FontSize(13).Bold().TextColor(palette.Warn).Padding(4, 0, 0, 0)
		for _, it := range missing {
			inst := it.Instance
			name := orText(inst.Label, inst.InstanceID)
			ui.Row(c.Key("miss-"+inst.InstanceID)).Gap(12).Padding(10, 14).Radius(10).Background(palette.Board).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Grow(1).Gap(2).Children(func() {
					ui.Text(c, name+" ("+inst.InstanceID+")").Bold().Selectable()
					muted(ui.Text(c, T("agent.forget.missing_roots", "roots", strings.Join(inst.MissingRoots, ", ")))).Wrap().Selectable()
				})
				actionWith(c, T("agent.forget.cleanup_btn"), T("need.valise"), func() {
					a.openAgentForget(host, inst.InstanceID, name)
				})
			})
		}
	})
}

func (a *App) agentForgetModal(c *ui.Context) {
	af := a.agentForget
	if af == nil {
		return
	}
	open := true
	if ui.Modal(c, &open, func() {
		dialog(ui.Column(c), 480).Children(func() {
			dialogTitle(ui.Text(c, T("agent.forget.title", "name", af.name)))
			notice(c, "warn", T("agent.forget.explain"))
			fact(c, T("agent.forget.target_label"), af.server+" / "+af.instance)
			if af.j.busy {
				a.jobLine(c, &af.j)
				return
			}
			if af.j.err != nil {
				errorBox(c, af.j.err, a.fold("agent-forget-err-"+af.server+"-"+af.instance))
			}
			ui.Row(c).Gap(8).AlignItems(ui.Center).Justify(ui.End).Children(func() {
				needTag(c, T("need.valise"))
				ui.Button(c, T("common.cancel")).OnClick(func() {
					a.agentForget = nil
				})
				dangerButton(c, T("agent.forget.confirm")).OnClick(func() {
					host, inst := af.server, af.instance
					fn := a.forgetInstanceFn
					if fn == nil {
						fn = a.svc.ForgetInstance
					}
					a.start(&af.j, 2*time.Minute, func(ctx context.Context) error {
						_, err := fn(ctx, host, inst)
						return err
					}, func(err error) {
						if err != nil {
							return
						}
						a.agentForget = nil
						a.forgetInstance(host, inst)
						a.removeAgentFromMemory(host, inst)
						if a.forgetInstanceFn == nil {
							a.refreshAgents()
						}
						curPath := a.router.Path()
						switch {
						case strings.HasPrefix(curPath, "/agents/"):
							a.navigate("/agents")
						case curPath == "/servers/"+host+"/agents":
							a.scanServer(host)
						}
					})
				})
			})
		})
	}).Dismissed() && !af.j.busy {
		a.agentForget = nil
	}
}

func (a *App) agentExists(host, instance string) bool {
	for _, it := range a.agents.value.Items {
		if it.ConnectionID == host && it.Instance.InstanceID == instance {
			return true
		}
	}
	return false
}

func (a *App) removeAgentFromMemory(host, instance string) {
	items := a.agents.value.Items
	out := make([]service.AgentSummary, 0, len(items))
	for _, it := range items {
		if it.ConnectionID == host && it.Instance.InstanceID == instance {
			continue
		}
		out = append(out, it)
	}
	a.agents.value.Items = out
}

// Keep reference to enroll used by forgetInstanceFn type in hooks.
var _ *enroll.InstanceForgetResult
